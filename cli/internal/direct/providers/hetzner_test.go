package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHetznerCreateServer(t *testing.T) {
	var createPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/ssh_keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"ssh_keys": []map[string]any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/ssh_keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"ssh_key": map[string]any{"name": "devopsellence-prod-1"}})
		case r.Method == http.MethodPost && r.URL.Path == "/servers":
			if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
				t.Fatalf("Authorization = %q", auth)
			}
			if err := json.NewDecoder(r.Body).Decode(&createPayload); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"server": map[string]any{
				"id":     42,
				"name":   "prod-1",
				"status": "running",
				"public_net": map[string]any{
					"ipv4": map[string]any{"ip": "203.0.113.10"},
				},
			}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	provider := &Hetzner{baseURL: server.URL, token: "test-token", client: server.Client()}
	got, err := provider.CreateServer(context.Background(), CreateServerInput{
		Name:         "prod-1",
		Region:       "ash",
		Size:         "cx22",
		SSHPublicKey: "ssh-ed25519 abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "42" || got.PublicIP != "203.0.113.10" || !provider.Ready(got) {
		t.Fatalf("server = %#v", got)
	}
	if createPayload["image"] != defaultHetznerImage {
		t.Fatalf("image = %v, want default", createPayload["image"])
	}
	keys := createPayload["ssh_keys"].([]any)
	if len(keys) != 1 || keys[0] != "devopsellence-prod-1" {
		t.Fatalf("ssh_keys = %#v", keys)
	}
}

func TestHetznerMissingToken(t *testing.T) {
	provider := &Hetzner{}
	_, err := provider.CreateServer(context.Background(), CreateServerInput{Name: "prod-1"})
	if err == nil || !strings.Contains(err.Error(), "HETZNER") {
		t.Fatalf("expected token error, got %v", err)
	}
}
