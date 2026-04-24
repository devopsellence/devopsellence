package systemimages

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/engine"
)

type fakeImageEngine struct {
	mu         sync.Mutex
	existing   map[string]bool
	pulled     []string
	pullErrors map[string]error
	inspectErr error
}

func (f *fakeImageEngine) ImageExists(ctx context.Context, image string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inspectErr != nil {
		return false, f.inspectErr
	}
	return f.existing[image], nil
}

func (f *fakeImageEngine) PullImage(ctx context.Context, image string, auth *engine.RegistryAuth) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.pullErrors[image]; err != nil {
		return err
	}
	f.pulled = append(f.pulled, image)
	f.existing[image] = true
	return nil
}

func TestPrefetcherPrefetchesEachImageOnce(t *testing.T) {
	engine := &fakeImageEngine{existing: map[string]bool{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	prefetcher := NewPrefetcher(engine, []string{"envoy", "envoy", " redis "}, logger)

	if err := prefetcher.Prefetch(context.Background()); err != nil {
		t.Fatalf("prefetch: %v", err)
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.pulled) != 2 {
		t.Fatalf("expected deduped pulls, got %v", engine.pulled)
	}
}

func TestPrefetcherContinuesAfterPullFailure(t *testing.T) {
	engine := &fakeImageEngine{
		existing:   map[string]bool{},
		pullErrors: map[string]error{"envoy": errors.New("pull failed")},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	prefetcher := NewPrefetcher(engine, []string{"envoy", "redis"}, logger)

	if err := prefetcher.Prefetch(context.Background()); err != nil {
		t.Fatalf("prefetch: %v", err)
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.pulled) != 1 || engine.pulled[0] != "redis" {
		t.Fatalf("expected prefetch to continue after failure, got %v", engine.pulled)
	}
}
