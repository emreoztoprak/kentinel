package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDatabaseFilesAreOwnerOnly guards the 0600 tightening on the SQLite
// file and its WAL/SHM sidecars — they hold the encrypted settings blob
// and insight history.
func TestDatabaseFilesAreOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "insights.db")
	s := NewPersistentStore(path, 90, 20, discardLog())
	defer s.Close()
	s.SaveSettings(sampleSettings()) // force WAL activity
	for _, f := range []string{path, path + "-wal"} {
		info, err := os.Stat(f)
		if err != nil {
			t.Fatalf("stat %s: %v", f, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %o, want 0600", f, perm)
		}
	}
}
