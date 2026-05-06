package file

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/report"
)

func TestReportWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	reporter := New(path, logger)

	status := report.Status{
		Time:     time.Now().UTC(),
		Phase:    report.PhaseSettled,
		Revision: "rev-1",
		Message:  "ok",
	}

	if err := reporter.Report(context.Background(), status); err != nil {
		t.Fatalf("report: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}

	var got report.Status
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Revision != status.Revision || got.Phase != status.Phase {
		t.Fatalf("unexpected status: %+v", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("unexpected permissions: %v", info.Mode().Perm())
	}
}

func TestReportTightensExistingDirectoryPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	reporter := New(filepath.Join(dir, "status.json"), logger)
	if err := reporter.Report(context.Background(), report.Status{Time: time.Now().UTC()}); err != nil {
		t.Fatalf("report: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if info.Mode().Perm() != 0o751 {
		t.Fatalf("unexpected dir permissions: %v", info.Mode().Perm())
	}
}
