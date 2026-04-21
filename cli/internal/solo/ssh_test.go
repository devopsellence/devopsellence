package solo

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/devopsellence/cli/internal/config"
)

func TestSSHArgsIncludeConnectTimeoutAndKey(t *testing.T) {
	node := config.SoloNode{
		User:   "root",
		Host:   "203.0.113.10",
		Port:   22,
		SSHKey: "/tmp/id_ed25519",
	}

	got := sshArgs(node, "true")
	want := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		"-p", "22",
		"-i", "/tmp/id_ed25519",
		"root@203.0.113.10",
		"true",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sshArgs() = %#v, want %#v", got, want)
	}
}

func TestSSHArgsUseManagedKnownHostsForProviderNodes(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	node := config.SoloNode{
		User:             "root",
		Host:             "203.0.113.10",
		Port:             22,
		SSHKey:           "/tmp/id_ed25519",
		Provider:         "hetzner",
		ProviderServerID: "123456",
	}

	sum := sha256.Sum256([]byte(node.Provider + "\x00" + node.ProviderServerID))
	got := sshArgs(node, "true")
	want := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		"-p", "22",
		"-o", "UserKnownHostsFile=" + filepath.Join(stateDir, "devopsellence", "ssh_known_hosts", "managed-"+hex.EncodeToString(sum[:])[:16]),
		"-i", "/tmp/id_ed25519",
		"root@203.0.113.10",
		"true",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sshArgs() = %#v, want %#v", got, want)
	}
}

func TestManagedKnownHostsPathHashesUntrustedServerID(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	node := config.SoloNode{
		Provider:         "hetzner",
		ProviderServerID: "../../escape",
	}

	path := managedKnownHostsPath(node)
	base := filepath.Join(stateDir, "devopsellence", "ssh_known_hosts")
	if filepath.Dir(path) != base {
		t.Fatalf("managedKnownHostsPath() dir = %q, want %q", filepath.Dir(path), base)
	}
	if filepath.Base(path) == node.Provider+"-"+node.ProviderServerID || filepath.Base(path) == "managed-"+node.ProviderServerID {
		t.Fatalf("managedKnownHostsPath() base = %q, want hashed filename", filepath.Base(path))
	}
}
