package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/diagnose"
	"github.com/devopsellence/devopsellence/agent/internal/version"
)

type staticTokens struct{}

func (staticTokens) ControlPlaneAccessToken() (string, time.Time) {
	return "node-access-token", time.Now().Add(time.Hour)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func TestClientClaimAndComplete(t *testing.T) {
	t.Parallel()

	claimCalls := 0
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if got := r.Header.Get("Authorization"); got != "Bearer node-access-token" {
				t.Fatalf("authorization = %q", got)
			}
			if got := r.Header.Get(version.CapabilitiesHeader); got != version.CapabilityHeaderValue() {
				t.Fatalf("capabilities = %q", got)
			}

			switch {
			case r.Method == http.MethodPost && r.URL.Path == claimPath:
				claimCalls++
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":17,"requested_at":"2026-03-29T20:00:00Z"}`)),
				}, nil
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agent/diagnose_requests/17/result":
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				result, ok := body["result"].(map[string]any)
				if !ok {
					t.Fatalf("result payload missing: %#v", body)
				}
				if result["agent_version"] != "devopsellence-agent/dev" {
					t.Fatalf("agent_version = %#v", result["agent_version"])
				}
				return &http.Response{
					StatusCode: http.StatusAccepted,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"status":"completed"}`)),
				}, nil
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			}
		}),
	}

	client, err := New(Config{
		BaseURL:    "https://control-plane.test",
		HTTPClient: httpClient,
		Tokens:     staticTokens{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request, err := client.Claim(context.Background())
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if request == nil || request.ID != 17 {
		t.Fatalf("request = %#v", request)
	}

	err = client.Complete(context.Background(), request.ID, diagnose.Result{
		CollectedAt:  "2026-03-29T20:00:02Z",
		AgentVersion: "devopsellence-agent/dev",
		Summary: diagnose.Summary{
			Status: "ok",
		},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
}
