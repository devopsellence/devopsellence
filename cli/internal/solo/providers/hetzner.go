package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	hetznerAPIBaseURL       = "https://api.hetzner.cloud/v1"
	defaultHetznerImage     = "ubuntu-24.04"
	defaultHetznerFirewall  = "devopsellence-solo"
	devopsellenceManagedKey = "devopsellence_managed"
)

type Hetzner struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewHetznerFromEnv() *Hetzner {
	return NewHetzner("")
}

func NewHetzner(token string) *Hetzner {
	token = strings.TrimSpace(token)
	if token == "" {
		token = firstEnv("DEVOPSELLENCE_HETZNER_API_TOKEN", "HCLOUD_TOKEN")
	}
	return &Hetzner{
		baseURL: hetznerAPIBaseURL,
		token:   token,
		client:  http.DefaultClient,
	}
}

func (h *Hetzner) Validate(ctx context.Context) error {
	if err := h.ensureConfigured(); err != nil {
		return err
	}
	var body struct {
		Locations []map[string]any `json:"locations"`
	}
	if err := h.doJSON(ctx, http.MethodGet, "/locations", nil, &body); err != nil {
		return fmt.Errorf("validate hetzner token: %w", err)
	}
	return nil
}

func (h *Hetzner) CreateServer(ctx context.Context, input CreateServerInput) (Server, error) {
	if err := h.ensureConfigured(); err != nil {
		return Server{}, err
	}
	sshKeyName := ""
	if strings.TrimSpace(input.SSHPublicKey) != "" {
		var err error
		sshKeyName, err = h.ensureSSHKey(ctx, input.SSHPublicKey)
		if err != nil {
			return Server{}, err
		}
	}
	firewallID, err := h.ensureFirewall(ctx)
	if err != nil {
		return Server{}, err
	}
	image := strings.TrimSpace(input.Image)
	if image == "" {
		image = defaultHetznerImage
	}
	labels := map[string]string{
		devopsellenceManagedKey: "true",
		"devopsellence_node":    input.Name,
	}
	for key, value := range input.Labels {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			labels[key] = value
		}
	}
	payload := map[string]any{
		"name":        input.Name,
		"server_type": input.Size,
		"location":    input.Region,
		"image":       image,
		"labels":      labels,
		"public_net": map[string]bool{
			"ipv4_enabled": true,
			"ipv6_enabled": true,
		},
	}
	if sshKeyName != "" {
		payload["ssh_keys"] = []string{sshKeyName}
	}
	if firewallID != nil {
		payload["firewalls"] = []map[string]any{{"firewall": firewallID}}
	}
	var body struct {
		Server map[string]any `json:"server"`
	}
	if err := h.doJSON(ctx, http.MethodPost, "/servers", payload, &body); err != nil {
		return Server{}, err
	}
	return parseHetznerServer(body.Server)
}

func (h *Hetzner) DeleteServer(ctx context.Context, providerServerID string) error {
	if err := h.ensureConfigured(); err != nil {
		return err
	}
	return h.doJSON(ctx, http.MethodDelete, "/servers/"+providerServerID, nil, nil)
}

func (h *Hetzner) GetServer(ctx context.Context, providerServerID string) (Server, error) {
	if err := h.ensureConfigured(); err != nil {
		return Server{}, err
	}
	var body struct {
		Server map[string]any `json:"server"`
	}
	if err := h.doJSON(ctx, http.MethodGet, "/servers/"+providerServerID, nil, &body); err != nil {
		return Server{}, err
	}
	return parseHetznerServer(body.Server)
}

func (h *Hetzner) Ready(server Server) bool {
	return server.Status == "running" && strings.TrimSpace(server.PublicIP) != ""
}

func (h *Hetzner) ensureConfigured() error {
	if strings.TrimSpace(h.token) == "" {
		return fmt.Errorf("run `devopsellence provider login hetzner` or configure DEVOPSELLENCE_HETZNER_API_TOKEN/HCLOUD_TOKEN for Hetzner servers")
	}
	return nil
}

func (h *Hetzner) ensureSSHKey(ctx context.Context, publicKey string) (string, error) {
	name := contentAddressedSSHKeyName(publicKey)
	normalizedPublicKey := canonicalSSHPublicKey(publicKey)
	var list struct {
		SSHKeys []map[string]any `json:"ssh_keys"`
	}
	if err := h.doJSON(ctx, http.MethodGet, "/ssh_keys", nil, &list); err != nil {
		return "", err
	}
	for _, key := range list.SSHKeys {
		if fmt.Sprint(key["name"]) == name {
			if canonicalSSHPublicKey(fmt.Sprint(key["public_key"])) != normalizedPublicKey {
				return "", fmt.Errorf("Hetzner SSH key %q exists with different public key content", name)
			}
			return name, nil
		}
	}
	var created struct {
		SSHKey map[string]any `json:"ssh_key"`
	}
	if err := h.doJSON(ctx, http.MethodPost, "/ssh_keys", map[string]any{
		"name":       name,
		"public_key": normalizedPublicKey,
	}, &created); err != nil {
		return "", err
	}
	return fmt.Sprint(created.SSHKey["name"]), nil
}

func contentAddressedSSHKeyName(publicKey string) string {
	sum := sha256.Sum256([]byte(canonicalSSHPublicKey(publicKey)))
	return "devopsellence-ssh-" + hex.EncodeToString(sum[:])[:16]
}

func canonicalSSHPublicKey(publicKey string) string {
	fields := strings.Fields(publicKey)
	if len(fields) >= 2 {
		return fields[0] + " " + fields[1]
	}
	return strings.TrimSpace(publicKey)
}

func (h *Hetzner) ensureFirewall(ctx context.Context) (any, error) {
	var list struct {
		Firewalls []map[string]any `json:"firewalls"`
	}
	if err := h.doJSON(ctx, http.MethodGet, "/firewalls?name="+defaultHetznerFirewall, nil, &list); err != nil {
		return "", err
	}
	for _, firewall := range list.Firewalls {
		if fmt.Sprint(firewall["name"]) == defaultHetznerFirewall {
			return firewall["id"], nil
		}
	}
	var created struct {
		Firewall map[string]any `json:"firewall"`
	}
	payload := map[string]any{
		"name": defaultHetznerFirewall,
		"labels": map[string]string{
			devopsellenceManagedKey: "true",
		},
		"rules": []map[string]any{
			hetznerFirewallRule("22"),
			hetznerFirewallRule("80"),
			hetznerFirewallRule("443"),
		},
	}
	if err := h.doJSON(ctx, http.MethodPost, "/firewalls", payload, &created); err != nil {
		return "", err
	}
	return created.Firewall["id"], nil
}

func hetznerFirewallRule(port string) map[string]any {
	return map[string]any{
		"direction":  "in",
		"protocol":   "tcp",
		"port":       port,
		"source_ips": []string{"0.0.0.0/0", "::/0"},
	}
}

func (h *Hetzner) doJSON(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.baseURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusNotFound && method == http.MethodDelete {
		return nil
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("hetzner API %s %s failed (%d): %s", method, path, res.StatusCode, string(data))
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode hetzner response: %w", err)
	}
	return nil
}

func parseHetznerServer(payload map[string]any) (Server, error) {
	if payload == nil {
		return Server{}, fmt.Errorf("hetzner response missing server")
	}
	id := fmt.Sprint(payload["id"])
	publicIP, _ := payloadValue(payload, "public_net", "ipv4", "ip").(string)
	return Server{
		ID:       id,
		Name:     stringValue(payload["name"]),
		Status:   stringValue(payload["status"]),
		PublicIP: publicIP,
		Raw:      payload,
	}, nil
}

func payloadValue(payload map[string]any, path ...string) any {
	var current any = payload
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[key]
	}
	return current
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
