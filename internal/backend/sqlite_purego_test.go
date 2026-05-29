package backend

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSQLiteImplementationUsesPureGo(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("sqlite_cgo.go"))
	if err != nil {
		t.Fatalf("read sqlite implementation: %v", err)
	}
	text := string(source)
	for _, forbidden := range []string{`import "C"`, "third_party/sqlite", "sqlite3.c"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("sqlite implementation still contains %q", forbidden)
		}
	}
}

func TestSQLiteOpensLegacyMappingsDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mappings.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	hashed, err := HashPassword("pass123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := db.exec(`
		CREATE TABLE id_mappings (
			virtual_id TEXT PRIMARY KEY,
			original_id TEXT NOT NULL,
			server_index INTEGER NOT NULL
		);
		CREATE TABLE id_additional_instances (
			virtual_id TEXT NOT NULL,
			original_id TEXT NOT NULL,
			server_index INTEGER NOT NULL,
			UNIQUE(virtual_id, original_id, server_index)
		);
		CREATE TABLE users (
			id TEXT PRIMARY KEY,
			username TEXT UNIQUE NOT NULL COLLATE NOCASE,
			password TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE user_servers (
			user_id TEXT NOT NULL,
			server_index INTEGER NOT NULL,
			PRIMARY KEY (user_id, server_index),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		);
		CREATE TABLE user_watch_progress (
			proxy_user_id TEXT NOT NULL,
			virtual_item_id TEXT NOT NULL,
			server_index INTEGER NOT NULL,
			original_item_id TEXT NOT NULL DEFAULT '',
			item_type TEXT NOT NULL DEFAULT '',
			series_virtual_id TEXT NOT NULL DEFAULT '',
			series_original_id TEXT NOT NULL DEFAULT '',
			series_name TEXT NOT NULL DEFAULT '',
			parent_index_number INTEGER NOT NULL DEFAULT 0,
			index_number INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			production_year INTEGER NOT NULL DEFAULT 0,
			provider_tmdb TEXT NOT NULL DEFAULT '',
			position_ticks INTEGER NOT NULL DEFAULT 0,
			runtime_ticks INTEGER NOT NULL DEFAULT 0,
			played INTEGER NOT NULL DEFAULT 0,
			is_favorite INTEGER NOT NULL DEFAULT 0,
			last_played INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (proxy_user_id, virtual_item_id)
		);
	`); err != nil {
		_ = closeSQLite(db)
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.execParams(`INSERT INTO id_mappings (virtual_id, original_id, server_index) VALUES (?, ?, ?)`, "virtual-movie", "movie-a", 0); err != nil {
		t.Fatalf("insert mapping: %v", err)
	}
	if err := db.execParams(`INSERT INTO id_additional_instances (virtual_id, original_id, server_index) VALUES (?, ?, ?)`, "virtual-movie", "movie-b", 1); err != nil {
		t.Fatalf("insert additional mapping: %v", err)
	}
	if err := db.execParams(`INSERT INTO users (id, username, password, enabled, created_at) VALUES (?, ?, ?, ?, ?)`, "user-1", "legacy", hashed, 1, int64(1234)); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.execParams(`INSERT INTO user_servers (user_id, server_index) VALUES (?, ?)`, "user-1", 1); err != nil {
		t.Fatalf("insert user server: %v", err)
	}
	if err := db.execParams(`INSERT INTO user_watch_progress (proxy_user_id, virtual_item_id, server_index, original_item_id, item_type, name, position_ticks, runtime_ticks, last_played) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, "user-1", "virtual-movie", 0, "movie-a", "Movie", "Legacy Movie", int64(50), int64(100), int64(2000)); err != nil {
		t.Fatalf("insert watch progress: %v", err)
	}
	if err := closeSQLite(db); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	logger := NewLogger(LogConfig{Level: "error", FileLevel: "error", DataDir: dir})
	t.Cleanup(func() { _ = logger.Close() })
	store, err := NewIDStore(dir, logger)
	if err != nil {
		t.Fatalf("open id store on legacy db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	resolved := store.ResolveVirtualID("virtual-movie")
	if resolved == nil || resolved.OriginalID != "movie-a" || resolved.ServerIndex != 0 || len(resolved.OtherInstances) != 1 {
		t.Fatalf("resolved legacy mapping = %#v", resolved)
	}

	userStore, err := NewUserStore(store.DB(), logger)
	if err != nil {
		t.Fatalf("open user store on legacy db: %v", err)
	}
	legacyUser := userStore.Authenticate("legacy", "pass123")
	if legacyUser == nil || len(legacyUser.AllowedServers) != 1 || legacyUser.AllowedServers[0] != 1 {
		t.Fatalf("legacy user = %#v", legacyUser)
	}

	watchStore, err := NewWatchStore(store.DB(), logger)
	if err != nil {
		t.Fatalf("open watch store on legacy db: %v", err)
	}
	progress := watchStore.GetProgress("user-1", "virtual-movie")
	if progress == nil || progress.Name != "Legacy Movie" || progress.PositionTicks != 50 {
		t.Fatalf("legacy progress = %#v", progress)
	}
	if err := watchStore.RecordProgress(&WatchProgress{ProxyUserID: "user-1", VirtualItemID: "virtual-movie", ServerIndex: 0, PositionTicks: 80, RuntimeTicks: 100}); err != nil {
		t.Fatalf("continue writing legacy progress: %v", err)
	}
}

func TestSQLiteSharedConnectionConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	logger := NewLogger(LogConfig{Level: "error", FileLevel: "error", DataDir: dir})
	t.Cleanup(func() { _ = logger.Close() })
	idStore, err := NewIDStore(dir, logger)
	if err != nil {
		t.Fatalf("new id store: %v", err)
	}
	t.Cleanup(func() { _ = idStore.Close() })
	userStore, err := NewUserStore(idStore.DB(), logger)
	if err != nil {
		t.Fatalf("new user store: %v", err)
	}
	watchStore, err := NewWatchStore(idStore.DB(), logger)
	if err != nil {
		t.Fatalf("new watch store: %v", err)
	}
	user, err := userStore.Create("concurrent", "pass123", []int{0, 1})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 90)
	for i := 0; i < 30; i++ {
		i := i
		wg.Add(3)
		go func() {
			defer wg.Done()
			virtualID := idStore.GetOrCreateVirtualID("item-concurrent-"+string(rune('a'+i%26)), i%2)
			idStore.AssociateAdditionalInstance(virtualID, "item-other", (i+1)%2)
		}()
		go func() {
			defer wg.Done()
			if got := userStore.Get(user.ID); got == nil {
				errs <- errString("missing concurrent user")
			}
		}()
		go func() {
			defer wg.Done()
			virtualID := "watch-concurrent-" + string(rune('a'+i%26))
			if err := watchStore.RecordProgress(&WatchProgress{ProxyUserID: user.ID, VirtualItemID: virtualID, ServerIndex: i % 2, PositionTicks: int64(i + 1), RuntimeTicks: 100}); err != nil {
				errs <- err
				return
			}
			if got := watchStore.GetProgress(user.ID, virtualID); got == nil {
				errs <- errString("missing concurrent progress")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
