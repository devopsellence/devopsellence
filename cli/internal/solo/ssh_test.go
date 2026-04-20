package solo

import (
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
