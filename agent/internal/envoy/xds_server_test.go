package envoy

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestXDSServerStartSetsSocketAccess(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	server := newXDSServer(logger, os.Getuid(), os.Getgid())
	socketPath := filepath.Join(t.TempDir(), "xds.sock")

	if err := server.Start(context.Background(), socketPath); err != nil {
		t.Fatalf("start xds server: %v", err)
	}
	t.Cleanup(func() {
		if server.grpcSrv != nil {
			server.grpcSrv.Stop()
		}
	})

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat xds socket: %v", err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("unexpected socket permissions: %v", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("expected syscall stat")
	}
	if int(stat.Uid) != os.Getuid() || int(stat.Gid) != os.Getgid() {
		t.Fatalf("unexpected socket ownership: %d:%d", stat.Uid, stat.Gid)
	}
}

func TestXDSServerStartTightensExistingDirectoryPermissions(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	server := newXDSServer(logger, os.Getuid(), os.Getgid())
	dir := filepath.Join(t.TempDir(), "envoy")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	socketPath := filepath.Join(dir, "xds.sock")
	if err := server.Start(context.Background(), socketPath); err != nil {
		t.Fatalf("start xds server: %v", err)
	}
	t.Cleanup(func() {
		if server.grpcSrv != nil {
			server.grpcSrv.Stop()
		}
	})

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("unexpected dir permissions: %v", info.Mode().Perm())
	}
}
