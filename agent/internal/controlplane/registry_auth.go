package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/devopsellence/devopsellence/agent/internal/httpx"
	"github.com/devopsellence/devopsellence/agent/internal/version"
)

type ControlPlaneAccessProvider interface {
	BaseURL() string
	ControlPlaneAccess(ctx context.Context) (string, time.Time, error)
}

type RegistryAuthProvider struct {
	tokens     ControlPlaneAccessProvider
	httpClient *http.Client
	now        func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	auth      *engine.RegistryAuth
	expiresAt time.Time
}

type registryAuthResponse struct {
	ServerAddress string `json:"server_address"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	ExpiresIn     int64  `json:"expires_in"`
}

func NewRegistryAuthProvider(tokens ControlPlaneAccessProvider, httpClient *http.Client) *RegistryAuthProvider {
	if httpClient == nil {
		httpClient = httpx.NewClient(10 * time.Second)
	}
	return &RegistryAuthProvider{
		tokens:     tokens,
		httpClient: httpClient,
		now:        time.Now,
		cache:      map[string]cacheEntry{},
	}
}

func (p *RegistryAuthProvider) AuthForImage(ctx context.Context, image string) (*engine.RegistryAuth, error) {
	host := registryHost(image)
	if host == "" {
		return nil, nil
	}

	if auth := p.cached(host); auth != nil {
		return auth, nil
	}

	token, _, err := p.tokens.ControlPlaneAccess(ctx)
	if err != nil {
		return nil, err
	}

	requestBody, err := json.Marshal(map[string]string{"image": image})
	if err != nil {
		return nil, fmt.Errorf("encode registry auth request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.tokens.BaseURL(), "/")+"/api/v1/agent/registry_auth", bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("build registry auth request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set(version.CapabilitiesHeader, version.CapabilityHeaderValue())

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry auth request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("registry auth request failed: http status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var response registryAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode registry auth response: %w", err)
	}

	auth := &engine.RegistryAuth{
		Username:      response.Username,
		Password:      response.Password,
		ServerAddress: response.ServerAddress,
	}
	p.store(host, auth, response.ExpiresIn)
	return auth, nil
}

func (p *RegistryAuthProvider) cached(host string) *engine.RegistryAuth {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.cache[host]
	if !ok {
		return nil
	}
	if !entry.expiresAt.IsZero() && !p.now().Before(entry.expiresAt) {
		delete(p.cache, host)
		return nil
	}
	return entry.auth
}

func (p *RegistryAuthProvider) store(host string, auth *engine.RegistryAuth, expiresIn int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry := cacheEntry{auth: auth}
	if expiresIn > 0 {
		entry.expiresAt = p.now().Add(time.Duration(expiresIn) * time.Second)
	}
	p.cache[host] = entry
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
