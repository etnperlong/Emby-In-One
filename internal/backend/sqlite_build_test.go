package backend

import "testing"

func TestSQLiteLoadExtensionUnavailable(t *testing.T) {
	db, err := openSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() {
		_ = closeSQLite(db)
	}()

	stmt, err := db.prepare("SELECT load_extension('not-a-real-extension')")
	if err != nil {
		return
	}
	defer stmt.finalize()

	if _, err := stmt.step(); err != nil {
		return
	}
	t.Fatal("sqlite load_extension unexpectedly succeeded")
}
