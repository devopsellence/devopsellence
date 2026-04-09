package lifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarkSatisfiedTightensExistingDirectoryPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	store := NewStore(filepath.Join(dir, "lifecycle-state.json"))
	if err := store.MarkSatisfied("web", 7, "abc"); err != nil {
		t.Fatalf("mark satisfied: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if info.Mode().Perm() != 0o751 {
		t.Fatalf("unexpected dir permissions: %v", info.Mode().Perm())
	}
}
