package gcp

import (
	"context"
	"strings"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/engine"
)

type GoogleAccessProvider interface {
	GoogleAccess(ctx context.Context) (string, time.Time, error)
}

type ArtifactRegistryAuthProvider struct {
	googleAccess GoogleAccessProvider
}

func NewArtifactRegistryAuthProvider(googleAccess GoogleAccessProvider) *ArtifactRegistryAuthProvider {
	return &ArtifactRegistryAuthProvider{googleAccess: googleAccess}
}

func (p *ArtifactRegistryAuthProvider) AuthForImage(ctx context.Context, image string) (*engine.RegistryAuth, error) {
	host := registryHost(image)
	if host == "" || !strings.HasSuffix(host, ".pkg.dev") {
		return nil, nil
	}

	token, _, err := p.googleAccess.GoogleAccess(ctx)
	if err != nil {
		return nil, err
	}
	return &engine.RegistryAuth{
		Username:      "oauth2accesstoken",
		Password:      token,
		ServerAddress: host,
	}, nil
}

func registryHost(image string) string {
	first, _, ok := strings.Cut(image, "/")
	if !ok {
		return ""
	}
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return ""
}
