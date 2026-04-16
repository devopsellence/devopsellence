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
	var firewallPayload map[string]any
	var sshKeyPayload map[string]any
	sshKeyName := contentAddressedSSHKeyName("ssh-ed25519 abc")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/ssh_keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"ssh_keys": []map[string]any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/ssh_keys":
			if err := json.NewDecoder(r.Body).Decode(&sshKeyPayload); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ssh_key": map[string]any{"name": sshKeyName}})
		case r.Method == http.MethodGet && r.URL.Path == "/firewalls":
			if r.URL.Query().Get("name") != defaultHetznerFirewall {
				t.Fatalf("firewall name query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"firewalls": []map[string]any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/firewalls":
			if err := json.NewDecoder(r.Body).Decode(&firewallPayload); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"firewall": map[string]any{"id": 99, "name": defaultHetznerFirewall}})
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
		Labels:       map[string]string{"devopsellence_project": "shop"},
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
	if firewallPayload["name"] != defaultHetznerFirewall {
		t.Fatalf("firewall name = %v, want default", firewallPayload["name"])
	}
	firewalls := createPayload["firewalls"].([]any)
	firewall := firewalls[0].(map[string]any)
	if firewall["firewall"] != float64(99) {
		t.Fatalf("firewalls = %#v, want id 99", firewalls)
	}
	labels := createPayload["labels"].(map[string]any)
	if labels["devopsellence_managed"] != "true" || labels["devopsellence_node"] != "prod-1" || labels["devopsellence_project"] != "shop" {
		t.Fatalf("labels = %#v", labels)
	}
	keys := createPayload["ssh_keys"].([]any)
	if len(keys) != 1 || keys[0] != sshKeyName {
		t.Fatalf("ssh_keys = %#v", keys)
	}
	if sshKeyPayload["name"] != sshKeyName || sshKeyPayload["public_key"] != "ssh-ed25519 abc" {
		t.Fatalf("ssh key payload = %#v", sshKeyPayload)
	}
}

func TestHetznerSSHKeyNameIgnoresComment(t *testing.T) {
	withComment := contentAddressedSSHKeyName("ssh-ed25519 abc alice@example")
	withoutComment := contentAddressedSSHKeyName("ssh-ed25519 abc")
	if withComment != withoutComment {
		t.Fatalf("key name with comment = %q, without = %q", withComment, withoutComment)
	}
}

func TestHetznerReusesContentAddressedSSHKey(t *testing.T) {
	sshKeyName := contentAddressedSSHKeyName("ssh-ed25519 abc")
	posted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/ssh_keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"ssh_keys": []map[string]any{{
				"name":       sshKeyName,
				"public_key": "ssh-ed25519 abc existing-comment",
			}}})
		case r.Method == http.MethodGet && r.URL.Path == "/firewalls":
			_ = json.NewEncoder(w).Encode(map[string]any{"firewalls": []map[string]any{{"id": 99, "name": defaultHetznerFirewall}}})
		case r.Method == http.MethodPost && r.URL.Path == "/ssh_keys":
			posted = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/servers":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			keys := payload["ssh_keys"].([]any)
			if len(keys) != 1 || keys[0] != sshKeyName {
				t.Fatalf("ssh_keys = %#v", keys)
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
	if _, err := provider.CreateServer(context.Background(), CreateServerInput{
		Name:         "prod-1",
		Region:       "ash",
		Size:         "cx22",
		SSHPublicKey: "ssh-ed25519 abc current-comment",
	}); err != nil {
		t.Fatal(err)
	}
	if posted {
		t.Fatal("posted duplicate ssh key")
	}
}

func TestHetznerValidateToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/locations" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"locations": []map[string]any{{"name": "ash"}}})
	}))
	defer server.Close()

	provider := &Hetzner{baseURL: server.URL, token: "test-token", client: server.Client()}
	if err := provider.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHetznerMissingToken(t *testing.T) {
	provider := &Hetzner{}
	_, err := provider.CreateServer(context.Background(), CreateServerInput{Name: "prod-1"})
	if err == nil || !strings.Contains(err.Error(), "HETZNER") {
		t.Fatalf("expected token error, got %v", err)
	}
}
