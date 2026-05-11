package solo

import (
	"context"
	"crypto/sha256"
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
	read   func(string) ([]byte, error)

	// cached state for change detection
	modTime time.Time
	size    int64
	digest  [sha256.Size]byte
	desired *authority.FetchResult
}

func New(path string, logger *slog.Logger) *Authority {
	return &Authority{
		path:   path,
		logger: logger,
		read:   os.ReadFile,
	}
}

func (a *Authority) Fetch(_ context.Context) (*authority.FetchResult, error) {
	info, err := os.Stat(a.path)
	if err != nil {
		if os.IsNotExist(err) {
			a.desired = nil
			return nil, authority.ErrNoDesiredState
		}
		return nil, fmt.Errorf("stat desired state file: %w", err)
	}

	read := a.read
	if read == nil {
		read = os.ReadFile
	}
	data, err := read(a.path)
	if err != nil {
		if os.IsNotExist(err) {
			a.desired = nil
			return nil, authority.ErrNoDesiredState
		}
		return nil, fmt.Errorf("read desired state file: %w", err)
	}
	digest := sha256.Sum256(data)

	// Return cached result if file content hasn't changed. The content digest
	// keeps rapid solo republish operations safe on filesystems with coarse
	// timestamp resolution.
	if a.desired != nil && info.ModTime().Equal(a.modTime) && info.Size() == a.size && digest == a.digest {
		return a.desired, nil
	}

	desired, present, err := desiredstatecache.ParseOverride(data)
	if err != nil {
		return nil, fmt.Errorf("load desired state: %w", err)
	}
	a.modTime = info.ModTime()
	a.size = info.Size()
	a.digest = digest
	if !present {
		a.desired = nil
		return nil, authority.ErrNoDesiredState
	}

	sequence := info.ModTime().UnixMilli()
	if a.desired != nil && sequence <= a.desired.Sequence {
		sequence = a.desired.Sequence + 1
	}
	a.desired = &authority.FetchResult{
		Desired:  desired,
		Sequence: sequence,
	}

	a.logger.Info("loaded desired state from file", "path", a.path, "revision", desired.GetRevision())
	return a.desired, nil
}
