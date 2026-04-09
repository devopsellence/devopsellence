package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devopsellence/devopsellence/agent/internal/fileaccess"
	"log/slog"

	"github.com/devopsellence/devopsellence/agent/internal/report"
)

type Reporter struct {
	path   string
	logger *slog.Logger
}

func New(path string, logger *slog.Logger) *Reporter {
	return &Reporter{path: path, logger: logger}
}

func (r *Reporter) Report(ctx context.Context, status report.Status) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}

	dir := filepath.Dir(r.path)
	if err := fileaccess.EnsureDirMode(dir, 0o751); err != nil {
		return fmt.Errorf("mkdir status dir: %w", err)
	}

	file, err := os.CreateTemp(dir, ".status-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp status: %w", err)
	}
	defer os.Remove(file.Name())

	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write status: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close status: %w", err)
	}

	if err := os.Rename(file.Name(), r.path); err != nil {
		return fmt.Errorf("rename status: %w", err)
	}

	if err := os.Chmod(r.path, 0o640); err != nil {
		return fmt.Errorf("chmod status: %w", err)
	}

	return nil
}
