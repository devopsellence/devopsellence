package remote

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

func TestFetchReadsDesiredStateFromControlPlaneHTTPAndUsesETag(t *testing.T) {
	serverState := newFakeRemoteServer()
	serverState.desiredETag = "standalone-etag-1"
	serverState.desiredStateURI = ""
	serverState.desiredPayload = []byte(`{
  "revision": "rev-http",
  "containers": [
    {
      "service_name": "web",
      "image": "ghcr.io/acme/apps/web:rev-1",
      "secret_refs": {
        "API_KEY": "__CONTROL_PLANE_SECRET__"
      }
    }
  ]
}`)
	server := httptest.NewServer(serverState.handler())
	defer server.Close()
	serverState.mu.Lock()
	serverState.desiredStateURI = server.URL + "/api/v1/agent/desired_state"
	serverState.desiredPayload = []byte(`{
  "revision": "rev-http",
  "containers": [
    {
      "service_name": "web",
      "image": "ghcr.io/acme/apps/web:rev-1",
      "secret_refs": {
        "API_KEY": "` + server.URL + `/api/v1/agent/secrets/environment_secrets/1"
      }
    }
  ]
}`)
	serverState.mu.Unlock()

	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{}, authManager, logger)

	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetchResult.Sequence != serverState.desiredSequence {
		t.Fatalf("unexpected fetch sequence: %d", fetchResult.Sequence)
	}
	if got := fetchResult.Desired.Containers[0].Env["API_KEY"]; got != "super-secret" {
		t.Fatalf("unexpected resolved secret: %q", got)
	}
	serverState.mu.Lock()
	if serverState.httpDesiredCalls != 1 || serverState.httpSecretCalls != 1 || serverState.secretCalls != 0 {
		t.Fatalf("unexpected first fetch counts: desired=%d http_secret=%d gsm_secret=%d", serverState.httpDesiredCalls, serverState.httpSecretCalls, serverState.secretCalls)
	}
	serverState.mu.Unlock()

	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	serverState.mu.Lock()
	defer serverState.mu.Unlock()
	if serverState.httpDesiredCalls != 2 || serverState.httpSecretCalls != 1 || serverState.secretCalls != 0 {
		t.Fatalf("expected etag revalidation without refetching secrets: desired=%d http_secret=%d gsm_secret=%d", serverState.httpDesiredCalls, serverState.httpSecretCalls, serverState.secretCalls)
	}
}

func TestFetchReadsStandaloneHTTPDesiredStateIngressSecret(t *testing.T) {
	serverState := newFakeRemoteServer()
	server := httptest.NewServer(serverState.handler())
	defer server.Close()
	serverState.mu.Lock()
	serverState.desiredStateURI = server.URL + "/api/v1/agent/desired_state"
	serverState.desiredPayload = []byte(`{
  "revision": "rev-http",
  "ingress": {
    "hosts": ["abc123.devopsellence.io"],
    "tunnel_token_secret_ref": "` + server.URL + `/api/v1/agent/secrets/environment_secrets/1"
  },
  "containers": []
}`)
	serverState.mu.Unlock()

	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{}, authManager, logger)

	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetchResult.Desired.Ingress == nil {
		t.Fatal("expected ingress payload")
	}
	if fetchResult.Desired.Ingress.TunnelToken != "super-secret" {
		t.Fatalf("unexpected ingress token: %q", fetchResult.Desired.Ingress.TunnelToken)
	}
	if fetchResult.Desired.Ingress.TunnelTokenSecretRef != "" {
		t.Fatalf("expected ingress secret ref to be cleared, got %q", fetchResult.Desired.Ingress.TunnelTokenSecretRef)
	}
}

func TestFetchKeepsDesiredStateProtoShapeWithHTTPSource(t *testing.T) {
	serverState := newFakeRemoteServer()
	server := httptest.NewServer(serverState.handler())
	defer server.Close()
	serverState.mu.Lock()
	serverState.desiredStateURI = server.URL + "/api/v1/agent/desired_state"
	serverState.desiredPayload = []byte(`{"revision":"rev-http","containers":[]}`)
	serverState.mu.Unlock()

	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{}, authManager, logger)

	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetchResult.Desired == nil {
		t.Fatal("expected desired state")
	}
	if _, ok := any(fetchResult.Desired).(*desiredstatepb.DesiredState); !ok {
		t.Fatal("expected protobuf desired state")
	}
}
