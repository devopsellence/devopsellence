package registryauth

import (
	"context"
	"log/slog"

	"github.com/devopsellence/devopsellence/agent/internal/engine"
)

type Provider interface {
	AuthForImage(ctx context.Context, image string) (*engine.RegistryAuth, error)
}

type MultiProvider struct {
	providers []Provider
	logger    *slog.Logger
}

func NewMultiProvider(logger *slog.Logger, providers ...Provider) *MultiProvider {
	return &MultiProvider{providers: providers, logger: logger}
}

func (p *MultiProvider) AuthForImage(ctx context.Context, image string) (*engine.RegistryAuth, error) {
	for _, provider := range p.providers {
		if provider == nil {
			continue
		}
		auth, err := provider.AuthForImage(ctx, image)
		if err != nil {
			p.logger.Warn("registry auth provider failed, trying next", "error", err, "image", image)
			continue
		}
		if auth != nil {
			return auth, nil
		}
	}
	return nil, nil
}
