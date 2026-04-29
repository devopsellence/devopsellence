package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreWriteReadDelete(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.json")
	store := New(path)

	if err := store.Write(map[string]any{"access_token": "token", "last_organization_id": 7}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %#o, want %#o", info.Mode().Perm(), 0o600)
	}

	state, err := store.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if state["access_token"] != "token" {
		t.Fatalf("access_token = %#v, want token", state["access_token"])
	}

	deleted, err := store.Delete()
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !deleted {
		t.Fatal("Delete() = false, want true")
	}
}

func TestDefaultPathHonorsDevopsellenceStateHome(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("DEVOPSELLENCE_STATE_HOME", stateHome)
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "xdg-state"))

	got := DefaultPath(filepath.Join("devopsellence", "solo", "state.json"))
	want := filepath.Join(stateHome, "devopsellence", "solo", "state.json")
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}
