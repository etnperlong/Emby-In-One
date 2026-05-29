package backend

import (
	"fmt"
	"strings"
	"sync"

	"zombiezen.com/go/sqlite"
)

type sqliteDB struct {
	mu   sync.Mutex
	conn *sqlite.Conn
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
	return &sqliteDB{conn: conn}, nil
}

func closeSQLite(db *sqliteDB) error {
	if db == nil {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.conn == nil {
		return nil
	}
	err := db.conn.Close()
	db.conn = nil
	return err
}

func (db *sqliteDB) exec(query string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.execLocked(query)
}

func (db *sqliteDB) execLocked(query string) error {
	if db == nil || db.conn == nil {
		return fmt.Errorf("sqlite exec: database is closed")
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
	db.mu.Lock()
	defer db.mu.Unlock()
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
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.prepareLocked(query)
}

func (db *sqliteDB) prepareLocked(query string) (*sqliteStmt, error) {
	if db == nil || db.conn == nil {
		return nil, fmt.Errorf("sqlite prepare: database is closed")
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
	stmt.db.mu.Lock()
	defer stmt.db.mu.Unlock()
	stmt.finalizeLocked()
}

func (stmt *sqliteStmt) finalizeLocked() {
	if stmt != nil && stmt.stmt != nil {
		_ = stmt.stmt.Finalize()
		stmt.stmt = nil
	}
}

func (stmt *sqliteStmt) bindAll(args ...any) error {
	stmt.db.mu.Lock()
	defer stmt.db.mu.Unlock()
	return stmt.bindAllLocked(args...)
}

func (stmt *sqliteStmt) bindAllLocked(args ...any) error {
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
	stmt.db.mu.Lock()
	defer stmt.db.mu.Unlock()
	if stmt == nil || stmt.stmt == nil {
		return false, fmt.Errorf("sqlite step: statement is finalized")
	}
	return stmt.stmt.Step()
}

func (stmt *sqliteStmt) columnText(index int) string {
	stmt.db.mu.Lock()
	defer stmt.db.mu.Unlock()
	if stmt == nil || stmt.stmt == nil {
		return ""
	}
	return stmt.stmt.ColumnText(index)
}

func (stmt *sqliteStmt) columnInt(index int) int {
	stmt.db.mu.Lock()
	defer stmt.db.mu.Unlock()
	if stmt == nil || stmt.stmt == nil {
		return 0
	}
	return stmt.stmt.ColumnInt(index)
}

func (stmt *sqliteStmt) columnInt64(index int) int64 {
	stmt.db.mu.Lock()
	defer stmt.db.mu.Unlock()
	if stmt == nil || stmt.stmt == nil {
		return 0
	}
	return stmt.stmt.ColumnInt64(index)
}
