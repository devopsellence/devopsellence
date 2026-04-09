package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeTokens struct {
	baseURL string
	calls   int
}

func (f *fakeTokens) BaseURL() string {
	return f.baseURL
}

func (f *fakeTokens) ControlPlaneAccess(ctx context.Context) (string, time.Time, error) {
	f.calls++
	return "cp-access", time.Now().Add(time.Hour), nil
}

func TestRegistryAuthProviderFetchesAndCachesControlPlaneRegistryAuth(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/registry_auth" {
			http.NotFound(w, r)
			return
		}
		requests++
		if got := r.Header.Get("Authorization"); got != "Bearer cp-access" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"server_address": "ghcr.io",
			"username":       "robot",
			"password":       "secret",
			"expires_in":     3600,
		})
	}))
	defer server.Close()

	tokens := &fakeTokens{baseURL: server.URL}
	provider := NewRegistryAuthProvider(tokens, server.Client())

	auth, err := provider.AuthForImage(context.Background(), "ghcr.io/acme/apps/web:rev-1")
	if err != nil {
		t.Fatalf("first auth: %v", err)
	}
	if auth == nil || auth.Username != "robot" || auth.Password != "secret" || auth.ServerAddress != "ghcr.io" {
		t.Fatalf("unexpected auth: %+v", auth)
	}
	auth, err = provider.AuthForImage(context.Background(), "ghcr.io/acme/apps/web:rev-1")
	if err != nil {
		t.Fatalf("second auth: %v", err)
	}
	if auth == nil {
		t.Fatal("expected cached auth")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if tokens.calls != 1 {
		t.Fatalf("control plane token calls = %d, want 1", tokens.calls)
	}
}
