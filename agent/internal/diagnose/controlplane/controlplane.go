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

	"github.com/devopsellence/devopsellence/agent/internal/diagnose"
	"github.com/devopsellence/devopsellence/agent/internal/version"
)

const claimPath = "/api/v1/agent/diagnose_requests/claim"

type tokenSource interface {
	ControlPlaneAccessToken() (string, time.Time)
}

type Config struct {
	BaseURL    string
	HTTPClient *http.Client
	Tokens     tokenSource
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	tokens     tokenSource
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("diagnose client requires base url")
	}
	if cfg.Tokens == nil {
		return nil, errors.New("diagnose client requires token source")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		httpClient: httpClient,
		tokens:     cfg.Tokens,
	}, nil
}

func (c *Client) Claim(ctx context.Context) (*diagnose.Request, error) {
	var request diagnose.Request
	err := c.doJSON(ctx, http.MethodPost, claimPath, nil, &request, http.StatusNoContent)
	if err != nil {
		if errors.Is(err, errNoContent) {
			return nil, nil
		}
		return nil, err
	}
	return &request, nil
}

func (c *Client) Complete(ctx context.Context, requestID int, result diagnose.Result) error {
	return c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v1/agent/diagnose_requests/%d/result", requestID), map[string]any{
		"result": result,
	}, nil)
}

func (c *Client) Fail(ctx context.Context, requestID int, message string) error {
	return c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v1/agent/diagnose_requests/%d/result", requestID), map[string]any{
		"error": message,
	}, nil)
}

var errNoContent = errors.New("no content")

func (c *Client) doJSON(ctx context.Context, method, path string, requestBody any, responseBody any, emptyStatusCodes ...int) error {
	token, _ := c.tokens.ControlPlaneAccessToken()
	if strings.TrimSpace(token) == "" {
		return errors.New("missing control plane access token")
	}

	var payload []byte
	var err error
	if requestBody != nil {
		payload, err = json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set(version.CapabilitiesHeader, version.CapabilityHeaderValue())
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	for _, statusCode := range emptyStatusCodes {
		if resp.StatusCode == statusCode {
			return errNoContent
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return fmt.Errorf("diagnose request failed: %s", message)
	}
	if responseBody == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(responseBody); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
