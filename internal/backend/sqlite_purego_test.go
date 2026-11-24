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
	serverIDs := []string{"srv-a", "srv-b"}
	user, err := userStore.Create("concurrent", "pass123", serverIDs)
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
			virtualID := idStore.GetOrCreateVirtualID("item-concurrent-"+string(rune('a'+i%26)), serverIDs[i%2])
			idStore.AssociateAdditionalInstance(virtualID, "item-other", serverIDs[(i+1)%2])
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
			if err := watchStore.RecordProgress(&WatchProgress{ProxyUserID: user.ID, VirtualItemID: virtualID, ServerID: serverIDs[i%2], PositionTicks: int64(i + 1), RuntimeTicks: 100}); err != nil {
				errs <- err
				return
			}
			got, err := watchStore.GetProgress(user.ID, virtualID)
			if err != nil {
				errs <- err
			} else if got == nil {
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
