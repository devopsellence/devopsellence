package solo

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
)

func TestSSHArgsIncludeConnectTimeoutAndKey(t *testing.T) {
	node := config.Node{
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
	node := config.Node{
		User:             "root",
		Host:             "203.0.113.10",
		Port:             22,
		SSHKey:           "/tmp/id_ed25519",
		Provider:         "hetzner",
		ProviderServerID: "123456",
	}

	got := sshArgs(node, "true")
	want := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		"-p", "22",
		"-o", "UserKnownHostsFile=" + managedKnownHostsPath(node),
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
	node := config.Node{
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
