package backend

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"zombiezen.com/go/sqlite"
)

type sqliteDB struct {
	// mu serializes every statement issued on this handle. All stores share one
	// SQLite connection, and both transactions and the cursor state of a
	// stepping SELECT are connection-scoped: without mutual exclusion a read
	// stepping rows while another store's transaction is open sees that
	// transaction's uncommitted rows (and its statement interleaving), and an
	// autocommit write issued while a transaction is open lands inside it and
	// is silently rolled back (or committed early) with it.
	//
	// Readers take the lock shared; single write statements and whole
	// transactions take it exclusive.
	mu sync.RWMutex

	// callMu serializes individual calls into the connection: a zombiezen Conn
	// and its statements are not safe for concurrent use, and readers holding
	// mu shared may step on the same Conn at once. Lock order is mu -> callMu.
	callMu sync.Mutex
	conn   *sqlite.Conn
}

type sqliteStmt struct {
	db   *sqliteDB
	stmt *sqlite.Stmt
}

func openSQLite(path string) (*sqliteDB, error) {
	conn, err := sqlite.OpenConn(path, sqlite.OpenReadWrite, sqlite.OpenCreate, sqlite.OpenPrivateCache)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	handle := &sqliteDB{conn: conn}
	// Defense in depth: the write lock above already serializes writers, but a busy
	// timeout keeps a second connection (or a future one) from failing instantly.
	if err := handle.exec(`PRAGMA busy_timeout = 5000`); err != nil {
		_ = closeSQLite(handle)
		return nil, fmt.Errorf("sqlite busy_timeout: %w", err)
	}
	return handle, nil
}

// errSQLiteClosed is returned by every entry point used after closeSQLite, so
// late readers/writers fail cleanly instead of using the closed handle.
var errSQLiteClosed = errors.New("sqlite: connection is closed")

func (db *sqliteDB) ensureOpen() error {
	if db == nil {
		return errors.New("sqlite: nil database handle")
	}
	if db.conn == nil {
		return errSQLiteClosed
	}
	return nil
}

// withReadLock runs fn while holding the connection's read lock. Use it for
// SELECT statements and their stepping loops: a read in progress must not
// interleave with another store's open transaction on this shared connection,
// or it can observe that transaction's uncommitted rows.
func (db *sqliteDB) withReadLock(fn func() error) error {
	if err := db.ensureOpen(); err != nil {
		return err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.conn == nil {
		// The close below takes the exclusive lock, so a read that entered
		// before it still owns the handle; one that starts after sees closed.
		return errSQLiteClosed
	}
	return fn()
}

// withWriteLock runs fn while holding the connection's write lock exclusively.
// Use it for single write statements so they cannot interleave with another
// store's open transaction or with a read in progress.
func (db *sqliteDB) withWriteLock(fn func() error) error {
	if err := db.ensureOpen(); err != nil {
		return err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.conn == nil {
		return errSQLiteClosed
	}
	return fn()
}

// withWriteTx runs fn inside BEGIN IMMEDIATE ... COMMIT under the connection's
// exclusive lock. Every multi-statement write must go through here so other
// stores cannot slip autocommit statements into (or lose theirs to) our
// transaction, and no read can step rows inside it.
//
// Lock order is store.mu -> db.mu. Nothing inside fn may call back into a
// store, so db.mu is only ever held for one statement, one transaction, or
// one read.
func (db *sqliteDB) withWriteTx(fn func() error) error {
	return db.withWriteLock(func() error {
		if err := db.exec(`BEGIN IMMEDIATE`); err != nil {
			return err
		}
		if err := fn(); err != nil {
			_ = db.exec(`ROLLBACK`)
			return err
		}
		if err := db.exec(`COMMIT`); err != nil {
			_ = db.exec(`ROLLBACK`)
			return err
		}
		return nil
	})
}

// writeParams runs a parameterized write statement under the connection write lock.
func (db *sqliteDB) writeParams(query string, args ...any) error {
	return db.withWriteLock(func() error { return db.execParams(query, args...) })
}

func closeSQLite(db *sqliteDB) error {
	if db == nil || db.conn == nil {
		return nil
	}
	// Close runs under the connection's exclusive lock (sqlite_cgo contract):
	// no reader or writer can be mid-statement, in-flight reads finish first,
	// and every later entry returns errSQLiteClosed instead of using the
	// closed handle. Callers converge: once conn is nil, Close is a no-op.
	db.mu.Lock()
	defer db.mu.Unlock()
	db.callMu.Lock()
	defer db.callMu.Unlock()
	if db.conn == nil {
		return nil
	}
	err := db.conn.Close()
	db.conn = nil
	return err
}

func (db *sqliteDB) exec(query string) error {
	if db == nil {
		return errSQLiteClosed
	}
	db.callMu.Lock()
	defer db.callMu.Unlock()
	return db.execLocked(query)
}

func (db *sqliteDB) execLocked(query string) error {
	if db.conn == nil {
		return errSQLiteClosed
	}
	for {
		query = strings.TrimSpace(query)
		if query == "" {
			return nil
		}
		stmt, trailingBytes, err := db.conn.PrepareTransient(query)
		if err != nil {
			return err
		}
		for {
			hasRow, err := stmt.Step()
			if err != nil {
				_ = stmt.Finalize()
				return err
			}
			if !hasRow {
				break
			}
		}
		if err := stmt.Finalize(); err != nil {
			return err
		}
		usedBytes := len(query) - trailingBytes
		if usedBytes <= 0 || usedBytes > len(query) {
			return fmt.Errorf("sqlite exec: invalid trailing byte count")
		}
		query = query[usedBytes:]
	}
}

// execParams runs a write query with parameterized arguments to prevent SQL injection.
func (db *sqliteDB) execParams(query string, args ...any) error {
	if db == nil {
		return errSQLiteClosed
	}
	db.callMu.Lock()
	defer db.callMu.Unlock()
	stmt, err := db.prepareLocked(query)
	if err != nil {
		return err
	}
	defer stmt.finalizeLocked()
	if err := stmt.bindAllLocked(args...); err != nil {
		return err
	}
	_, err = stmt.stmt.Step()
	return err
}

func (db *sqliteDB) prepare(query string) (*sqliteStmt, error) {
	if db == nil {
		return nil, errSQLiteClosed
	}
	db.callMu.Lock()
	defer db.callMu.Unlock()
	return db.prepareLocked(query)
}

func (db *sqliteDB) prepareLocked(query string) (*sqliteStmt, error) {
	if db.conn == nil {
		return nil, errSQLiteClosed
	}
	stmt, trailingBytes, err := db.conn.PrepareTransient(query)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(query[len(query)-trailingBytes:]) != "" {
		_ = stmt.Finalize()
		return nil, fmt.Errorf("sqlite prepare: statement has trailing SQL")
	}
	return &sqliteStmt{db: db, stmt: stmt}, nil
}

func (stmt *sqliteStmt) finalize() {
	if stmt == nil || stmt.db == nil {
		return
	}
	stmt.db.callMu.Lock()
	defer stmt.db.callMu.Unlock()
	stmt.finalizeLocked()
}

func (stmt *sqliteStmt) finalizeLocked() {
	if stmt != nil && stmt.stmt != nil {
		_ = stmt.stmt.Finalize()
		stmt.stmt = nil
	}
}

func (stmt *sqliteStmt) bindAll(args ...any) error {
	stmt.db.callMu.Lock()
	defer stmt.db.callMu.Unlock()
	return stmt.bindAllLocked(args...)
}

func (stmt *sqliteStmt) bindAllLocked(args ...any) error {
	if stmt.stmt == nil {
		return fmt.Errorf("sqlite bind: statement is finalized")
	}
	for i, arg := range args {
		param := i + 1
		switch v := arg.(type) {
		case string:
			stmt.stmt.BindText(param, v)
		case int:
			stmt.stmt.BindInt64(param, int64(v))
		case int64:
			stmt.stmt.BindInt64(param, v)
		default:
			return fmt.Errorf("unsupported sqlite bind type %T", arg)
		}
	}
	return nil
}

func (stmt *sqliteStmt) step() (bool, error) {
	if stmt == nil || stmt.db == nil {
		return false, fmt.Errorf("sqlite step: statement is finalized")
	}
	stmt.db.callMu.Lock()
	defer stmt.db.callMu.Unlock()
	if stmt.stmt == nil {
		return false, fmt.Errorf("sqlite step: statement is finalized")
	}
	return stmt.stmt.Step()
}

func (stmt *sqliteStmt) columnText(index int) string {
	if stmt == nil || stmt.db == nil {
		return ""
	}
	stmt.db.callMu.Lock()
	defer stmt.db.callMu.Unlock()
	if stmt.stmt == nil {
		return ""
	}
	return stmt.stmt.ColumnText(index)
}

func (stmt *sqliteStmt) columnInt(index int) int {
	if stmt == nil || stmt.db == nil {
		return 0
	}
	stmt.db.callMu.Lock()
	defer stmt.db.callMu.Unlock()
	if stmt.stmt == nil {
		return 0
	}
	return stmt.stmt.ColumnInt(index)
}

func (stmt *sqliteStmt) columnInt64(index int) int64 {
	if stmt == nil || stmt.db == nil {
		return 0
	}
	stmt.db.callMu.Lock()
	defer stmt.db.callMu.Unlock()
	if stmt.stmt == nil {
		return 0
	}
	return stmt.stmt.ColumnInt64(index)
}
