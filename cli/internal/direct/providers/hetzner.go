package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	hetznerAPIBaseURL   = "https://api.hetzner.cloud/v1"
	defaultHetznerImage = "ubuntu-24.04"
)

type Hetzner struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewHetznerFromEnv() *Hetzner {
	return &Hetzner{
		baseURL: hetznerAPIBaseURL,
		token:   firstEnv("DEVOPSELLENCE_HETZNER_API_TOKEN", "HCLOUD_TOKEN"),
		client:  http.DefaultClient,
	}
}

func (h *Hetzner) CreateServer(ctx context.Context, input CreateServerInput) (Server, error) {
	if err := h.ensureConfigured(); err != nil {
		return Server{}, err
	}
	sshKeyName := ""
	if strings.TrimSpace(input.SSHPublicKey) != "" {
		var err error
		sshKeyName, err = h.ensureSSHKey(ctx, input.Name, input.SSHPublicKey)
		if err != nil {
			return Server{}, err
		}
	}
	image := strings.TrimSpace(input.Image)
	if image == "" {
		image = defaultHetznerImage
	}
	payload := map[string]any{
		"name":        input.Name,
		"server_type": input.Size,
		"location":    input.Region,
		"image":       image,
		"public_net": map[string]bool{
			"ipv4_enabled": true,
			"ipv6_enabled": true,
		},
	}
	if sshKeyName != "" {
		payload["ssh_keys"] = []string{sshKeyName}
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
		return fmt.Errorf("configure DEVOPSELLENCE_HETZNER_API_TOKEN or HCLOUD_TOKEN for direct Hetzner servers")
	}
	return nil
}

func (h *Hetzner) ensureSSHKey(ctx context.Context, nodeName, publicKey string) (string, error) {
	name := "devopsellence-" + nodeName
	var list struct {
		SSHKeys []map[string]any `json:"ssh_keys"`
	}
	if err := h.doJSON(ctx, http.MethodGet, "/ssh_keys", nil, &list); err != nil {
		return "", err
	}
	for _, key := range list.SSHKeys {
		if fmt.Sprint(key["name"]) == name {
			return name, nil
		}
	}
	var created struct {
		SSHKey map[string]any `json:"ssh_key"`
	}
	if err := h.doJSON(ctx, http.MethodPost, "/ssh_keys", map[string]any{
		"name":       name,
		"public_key": publicKey,
	}, &created); err != nil {
		return "", err
	}
	return fmt.Sprint(created.SSHKey["name"]), nil
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
	if err := json.Unmarshal(data, out); err != nil {
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
