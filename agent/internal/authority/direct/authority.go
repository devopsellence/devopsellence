package direct

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/authority"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatecache"
)

// Authority watches a local desired-state override file and returns it as-is.
// No secret resolution — secrets are pre-resolved by the CLI before writing.
type Authority struct {
	path   string
	logger *slog.Logger

	// cached state for change detection
	modTime time.Time
	size    int64
	desired *authority.FetchResult
}

func New(path string, logger *slog.Logger) *Authority {
	return &Authority{
		path:   path,
		logger: logger,
	}
}

func (a *Authority) Fetch(_ context.Context) (*authority.FetchResult, error) {
	info, err := os.Stat(a.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, authority.ErrNoDesiredState
		}
		return nil, fmt.Errorf("stat desired state file: %w", err)
	}

	// Return cached result if file hasn't changed.
	if a.desired != nil && info.ModTime().Equal(a.modTime) && info.Size() == a.size {
		return a.desired, nil
	}

	desired, present, err := desiredstatecache.LoadOverride(a.path)
	if err != nil {
		return nil, fmt.Errorf("load desired state: %w", err)
	}
	if !present {
		return nil, authority.ErrNoDesiredState
	}

	a.modTime = info.ModTime()
	a.size = info.Size()
	a.desired = &authority.FetchResult{
		Desired:  desired,
		Sequence: info.ModTime().UnixMilli(),
	}

	a.logger.Info("loaded desired state from file", "path", a.path, "revision", desired.GetRevision())
	return a.desired, nil
}
