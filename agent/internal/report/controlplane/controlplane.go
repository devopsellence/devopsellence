package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/report"
	"github.com/devopsellence/devopsellence/agent/internal/version"
)

const statusPath = "/api/v1/agent/status"

type tokenSource interface {
	ControlPlaneAccessToken() (string, time.Time)
}

type Config struct {
	BaseURL    string
	HTTPClient *http.Client
	Tokens     tokenSource
}

type Reporter struct {
	baseURL    string
	httpClient *http.Client
	tokens     tokenSource
}

func New(cfg Config) (*Reporter, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("control plane reporter requires base url")
	}
	if cfg.Tokens == nil {
		return nil, errors.New("control plane reporter requires token source")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Reporter{
		baseURL:    strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		httpClient: httpClient,
		tokens:     cfg.Tokens,
	}, nil
}

func (r *Reporter) Report(ctx context.Context, status report.Status) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	token, _ := r.tokens.ControlPlaneAccessToken()
	if strings.TrimSpace(token) == "" {
		return errors.New("missing control plane access token")
	}

	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+statusPath, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set(version.CapabilitiesHeader, version.CapabilityHeaderValue())

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return fmt.Errorf("status request failed: %s", message)
	}

	return nil
}
