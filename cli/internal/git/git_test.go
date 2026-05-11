package git

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCurrentSHASuggestsInitialCommitForFreshRepo(t *testing.T) {
	root := t.TempDir()
	if err := exec.Command("git", "-C", root, "init").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	_, err := (Client{}).CurrentSHA(root)
	if err == nil {
		t.Fatal("CurrentSHA() error = nil, want fresh repo error")
	}
	for _, want := range []string{"git add .", "git commit -m 'initial deploy'"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("CurrentSHA() error = %v, want %q", err, want)
		}
	}
}
