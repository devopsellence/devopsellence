package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

const (
	defaultDirectoryURL = lego.LEDirectoryProduction
	defaultBindAddress  = "0.0.0.0:15980"
	nodePeerHeader      = "devopsellence-node-peer"
)

type Config struct {
	CertPath    string
	KeyPath     string
	AccountPath string
	BindAddress string
	FileUID     int
	FileGID     int
	RenewBefore time.Duration
	Logger      *slog.Logger
}

type Manager struct {
	cfg      Config
	logger   *slog.Logger
	provider *HTTP01Provider
	once     sync.Once
	startErr error
}

func New(cfg Config) *Manager {
	if cfg.BindAddress == "" {
		cfg.BindAddress = defaultBindAddress
	}
	if cfg.RenewBefore <= 0 {
		cfg.RenewBefore = 30 * 24 * time.Hour
	}
	if cfg.AccountPath == "" && cfg.CertPath != "" {
		cfg.AccountPath = filepath.Join(filepath.Dir(cfg.CertPath), "acme-account.json")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		cfg:      cfg,
		logger:   logger,
		provider: NewHTTP01Provider(),
	}
}

func (m *Manager) Ensure(ctx context.Context, ingress *desiredstatepb.Ingress, nodePeers []*desiredstatepb.NodePeer) error {
	if !needsAutoTLS(ingress) {
		return nil
	}
	hosts := ingressHosts(ingress)
	if len(hosts) == 0 {
		return fmt.Errorf("acme: ingress hosts required")
	}
	if certCurrent(m.cfg.CertPath, hosts, m.cfg.RenewBefore) {
		return nil
	}
	if strings.TrimSpace(m.cfg.CertPath) == "" || strings.TrimSpace(m.cfg.KeyPath) == "" {
		return fmt.Errorf("acme: cert and key paths are required")
	}
	if err := m.startHTTP01Server(); err != nil {
		return err
	}
	m.provider.SetPeers(nodePeerPublicWebAddresses(nodePeers))

	user, err := m.loadOrCreateUser(ingress)
	if err != nil {
		return err
	}
	legoCfg := lego.NewConfig(user)
	legoCfg.CADirURL = firstNonEmpty(ingressCADirectoryURL(ingress), defaultDirectoryURL)
	legoCfg.Certificate.KeyType = certcrypto.EC256
	client, err := lego.NewClient(legoCfg)
	if err != nil {
		return fmt.Errorf("acme: create client: %w", err)
	}
	if err := client.Challenge.SetHTTP01Provider(m.provider); err != nil {
		return fmt.Errorf("acme: configure http-01 provider: %w", err)
	}
	if user.Registration == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return fmt.Errorf("acme: register account: %w", err)
		}
		user.Registration = reg
		if err := m.saveUser(user); err != nil {
			return err
		}
	}
	result, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: hosts,
		Bundle:  true,
	})
	if err != nil {
		return fmt.Errorf("acme: obtain certificate for %s: %w", strings.Join(hosts, ","), err)
	}
	if err := writePEMFile(m.cfg.CertPath, result.Certificate, m.cfg.FileUID, m.cfg.FileGID); err != nil {
		return fmt.Errorf("acme: write cert: %w", err)
	}
	if err := writePEMFile(m.cfg.KeyPath, result.PrivateKey, m.cfg.FileUID, m.cfg.FileGID); err != nil {
		return fmt.Errorf("acme: write key: %w", err)
	}
	m.logger.Info("acme certificate ready", "hosts", strings.Join(hosts, ","))
	return nil
}

func (m *Manager) startHTTP01Server() error {
	m.once.Do(func() {
		listener, err := net.Listen("tcp", m.cfg.BindAddress)
		if err != nil {
			m.startErr = fmt.Errorf("acme: listen http-01: %w", err)
			return
		}
		server := &http.Server{Handler: m.provider}
		go func() {
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				m.logger.Error("acme http-01 server stopped", "error", err)
			}
		}()
		m.logger.Info("acme http-01 server started", "addr", listener.Addr().String())
	})
	return m.startErr
}

func (m *Manager) loadOrCreateUser(ingress *desiredstatepb.Ingress) (*User, error) {
	if data, err := os.ReadFile(m.cfg.AccountPath); err == nil {
		var stored storedAccount
		if err := json.Unmarshal(data, &stored); err != nil {
			return nil, fmt.Errorf("acme: parse account: %w", err)
		}
		key, err := parseECPrivateKey(stored.PrivateKeyPEM)
		if err != nil {
			return nil, err
		}
		return &User{Email: stored.Email, Registration: stored.Registration, key: key}, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("acme: read account: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("acme: generate account key: %w", err)
	}
	return &User{Email: ingressTLSEmail(ingress), key: key}, nil
}

func (m *Manager) saveUser(user *User) error {
	keyPEM, err := marshalECPrivateKey(user.key)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(storedAccount{
		Email:         user.Email,
		PrivateKeyPEM: keyPEM,
		Registration:  user.Registration,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("acme: marshal account: %w", err)
	}
	return writePEMFile(m.cfg.AccountPath, data, m.cfg.FileUID, m.cfg.FileGID)
}

type User struct {
	Email        string
	Registration *registration.Resource
	key          crypto.PrivateKey
}

func (u *User) GetEmail() string {
	return u.Email
}

func (u *User) GetRegistration() *registration.Resource {
	return u.Registration
}

func (u *User) GetPrivateKey() crypto.PrivateKey {
	return u.key
}

type storedAccount struct {
	Email         string                 `json:"email,omitempty"`
	PrivateKeyPEM []byte                 `json:"private_key_pem"`
	Registration  *registration.Resource `json:"registration,omitempty"`
}

type HTTP01Provider struct {
	mu     sync.RWMutex
	values map[string]string
	peers  []string
	client *http.Client
}

func NewHTTP01Provider() *HTTP01Provider {
	return &HTTP01Provider{
		values: map[string]string{},
		client: &http.Client{Timeout: 2 * time.Second},
	}
}

func (p *HTTP01Provider) SetPeers(peers []string) {
	normalized := normalizeNodePeerAddresses(peers)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.peers = normalized
}

func (p *HTTP01Provider) Present(_ string, token string, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.values[token] = keyAuth
	return nil
}

func (p *HTTP01Provider) CleanUp(_ string, token string, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.values, token)
	return nil
}

func (p *HTTP01Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/.well-known/acme-challenge/"
	if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, prefix)
	p.mu.RLock()
	value, ok := p.values[token]
	p.mu.RUnlock()
	if !ok {
		if r.Header.Get(nodePeerHeader) != "1" {
			if value, ok := p.fetchPeerChallenge(r.Context(), r.URL.Path); ok {
				w.Header().Set("content-type", "text/plain")
				_, _ = w.Write([]byte(value))
				return
			}
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("content-type", "text/plain")
	_, _ = w.Write([]byte(value))
}

func (p *HTTP01Provider) fetchPeerChallenge(ctx context.Context, path string) (string, bool) {
	p.mu.RLock()
	peers := append([]string(nil), p.peers...)
	client := p.client
	p.mu.RUnlock()

	for _, peer := range peers {
		target := peerChallengeURL(peer, path)
		if target == "" {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			continue
		}
		req.Header.Set(nodePeerHeader, "1")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		value := strings.TrimSpace(string(body))
		if value != "" {
			return value, true
		}
	}
	return "", false
}

func peerChallengeURL(peer, path string) string {
	peer = strings.TrimSpace(peer)
	if peer == "" {
		return ""
	}
	if !strings.Contains(peer, "://") {
		if strings.Count(peer, ":") > 1 && !strings.HasPrefix(peer, "[") {
			peer = "[" + peer + "]"
		}
		peer = "http://" + peer
	}
	u, err := url.Parse(peer)
	if err != nil || u.Host == "" {
		return ""
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func needsAutoTLS(ingress *desiredstatepb.Ingress) bool {
	if ingress == nil {
		return false
	}
	mode := strings.TrimSpace(ingress.Mode)
	if mode == "" {
		if strings.TrimSpace(ingress.TunnelToken) != "" || strings.TrimSpace(ingress.TunnelTokenSecretRef) != "" {
			return false
		}
		mode = "public"
	}
	if mode != "public" {
		return false
	}
	tls := ingress.GetTls()
	if tls == nil {
		return true
	}
	mode = strings.TrimSpace(tls.Mode)
	return mode == "" || mode == "auto"
}

func ingressTLSEmail(ingress *desiredstatepb.Ingress) string {
	if ingress == nil || ingress.GetTls() == nil {
		return ""
	}
	return strings.TrimSpace(ingress.GetTls().GetEmail())
}

func ingressCADirectoryURL(ingress *desiredstatepb.Ingress) string {
	if ingress == nil || ingress.GetTls() == nil {
		return ""
	}
	return strings.TrimSpace(ingress.GetTls().GetCaDirectoryUrl())
}

func nodePeerPublicWebAddresses(peers []*desiredstatepb.NodePeer) []string {
	normalized := make([]string, 0, len(peers))
	for _, peer := range peers {
		if peer == nil || !nodePeerHasLabel(peer, "web") {
			continue
		}
		normalized = append(normalized, peer.GetPublicAddress())
	}
	return normalizeNodePeerAddresses(normalized)
}

func normalizeNodePeerAddresses(peers []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(peers))
	for _, peer := range peers {
		peer = strings.TrimSpace(peer)
		if peer == "" || seen[peer] {
			continue
		}
		seen[peer] = true
		normalized = append(normalized, peer)
	}
	sort.Strings(normalized)
	return normalized
}

func nodePeerHasLabel(peer *desiredstatepb.NodePeer, want string) bool {
	for _, label := range peer.GetLabels() {
		if strings.TrimSpace(label) == want {
			return true
		}
	}
	return false
}

func ingressHosts(ingress *desiredstatepb.Ingress) []string {
	seen := map[string]bool{}
	hosts := []string{}
	for _, host := range ingress.GetHosts() {
		host = strings.TrimSpace(host)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

func certCurrent(path string, hosts []string, renewBefore time.Duration) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if time.Until(cert.NotAfter) <= renewBefore {
		return false
	}
	names := map[string]bool{}
	for _, name := range cert.DNSNames {
		names[name] = true
	}
	if cert.Subject.CommonName != "" {
		names[cert.Subject.CommonName] = true
	}
	for _, host := range hosts {
		if !names[host] {
			return false
		}
	}
	return true
}

func parseECPrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("acme: account private key PEM missing")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("acme: parse account private key: %w", err)
	}
	return key, nil
}

func marshalECPrivateKey(key crypto.PrivateKey) ([]byte, error) {
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("acme: expected ECDSA account key")
	}
	der, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		return nil, fmt.Errorf("acme: marshal account key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func writePEMFile(path string, data []byte, uid, gid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return err
	}
	if uid >= 0 || gid >= 0 {
		if err := os.Chown(tmp.Name(), uid, gid); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), path)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
