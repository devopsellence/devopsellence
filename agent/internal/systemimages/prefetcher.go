package systemimages

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/devopsellence/devopsellence/agent/internal/engine"
)

type ImageEngine interface {
	ImageExists(ctx context.Context, image string) (bool, error)
	PullImage(ctx context.Context, image string, auth *engine.RegistryAuth) error
}

type Prefetcher struct {
	engine ImageEngine
	images []string
	logger *slog.Logger
}

func NewPrefetcher(engine ImageEngine, images []string, logger *slog.Logger) *Prefetcher {
	if logger == nil {
		logger = slog.Default()
	}

	normalized := make([]string, 0, len(images))
	seen := map[string]struct{}{}
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		normalized = append(normalized, image)
	}

	return &Prefetcher{
		engine: engine,
		images: normalized,
		logger: logger,
	}
}

func (p *Prefetcher) Prefetch(ctx context.Context) error {
	if p.engine == nil || len(p.images) == 0 {
		return nil
	}

	for _, image := range p.images {
		if err := ensureImage(ctx, p.engine, image); err != nil {
			p.logger.Warn("system image prefetch failed", "image", image, "error", err)
			continue
		}

		p.logger.Info("system image prefetched", "image", image)
	}
	return nil
}

func ensureImage(ctx context.Context, engine ImageEngine, image string) error {
	exists, err := engine.ImageExists(ctx, image)
	if err != nil {
		return fmt.Errorf("inspect image %s: %w", image, err)
	}
	if exists {
		return nil
	}
	if err := engine.PullImage(ctx, image, nil); err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	return nil
}
