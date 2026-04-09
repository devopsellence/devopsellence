package origincert

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/version"
)

const (
	issuancePath         = "/api/v1/agent/ingress_certificates"
	ingressModeDirectDNS = "direct_dns"
	defaultHTTPTimeout   = 2 * time.Minute
)

var issuanceBackoffSchedule = []time.Duration{
	15 * time.Second,
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
}

type tokenSource interface {
	ControlPlaneAccessToken() (string, time.Time)
}

type Config struct {
	BaseURL     string
	CertPath    string
	KeyPath     string
	FileUID     int
	FileGID     int
	RenewBefore time.Duration
	HTTPClient  *http.Client
	Tokens      tokenSource
}

type Manager struct {
	baseURL       string
	certPath      string
	keyPath       string
	fileUID       int
	fileGID       int
	renewBefore   time.Duration
	httpClient    *http.Client
	tokens        tokenSource
	now           func() time.Time
	mu            sync.Mutex
	backoffByHost map[string]issuanceBackoff
}

type issuanceResponse struct {
	CertificatePEM string `json:"certificate_pem"`
	Hostname       string `json:"hostname"`
}

type issuanceErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type issuanceBackoff struct {
	Until    time.Time
	Attempts int
	Message  string
}

type retryAfterError struct {
	message    string
	retryAfter time.Time
}

func (e *retryAfterError) Error() string {
	return e.message
}

func New(cfg Config) (*Manager, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("ingress cert manager requires base url")
	}
	if strings.TrimSpace(cfg.CertPath) == "" {
		return nil, errors.New("ingress cert manager requires cert path")
	}
	if strings.TrimSpace(cfg.KeyPath) == "" {
		return nil, errors.New("ingress cert manager requires key path")
	}
	if cfg.Tokens == nil {
		return nil, errors.New("ingress cert manager requires token source")
	}
	if cfg.FileUID < 0 {
		return nil, errors.New("ingress cert manager file uid cannot be negative")
	}
	if cfg.FileGID < 0 {
		return nil, errors.New("ingress cert manager file gid cannot be negative")
	}
	if cfg.RenewBefore < 0 {
		return nil, errors.New("ingress cert renew-before cannot be negative")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}

	return &Manager{
		baseURL:       strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		certPath:      strings.TrimSpace(cfg.CertPath),
		keyPath:       strings.TrimSpace(cfg.KeyPath),
		fileUID:       cfg.FileUID,
		fileGID:       cfg.FileGID,
		renewBefore:   cfg.RenewBefore,
		httpClient:    httpClient,
		tokens:        cfg.Tokens,
		now:           time.Now,
		backoffByHost: map[string]issuanceBackoff{},
	}, nil
}

func (m *Manager) Ensure(ctx context.Context, ingress *desiredstatepb.Ingress) error {
	if normalizedIngressMode(ingress) != ingressModeDirectDNS {
		return nil
	}

	hostname := strings.TrimSpace(ingress.GetHostname())
	if hostname == "" {
		return errors.New("ingress cert manager requires ingress hostname")
	}

	valid, err := m.currentCertificateValid(hostname)
	if err == nil && valid {
		if err := m.ensureInstalledFileAccess(); err != nil {
			return fmt.Errorf("ensure ingress certificate file access: %w", err)
		}
		m.clearBackoff(hostname)
		return nil
	}
	if err := m.activeBackoff(hostname); err != nil {
		return err
	}

	keyPEM, csrPEM, key, err := generateCSR(hostname)
	if err != nil {
		return fmt.Errorf("generate ingress csr: %w", err)
	}

	certificatePEM, err := m.issueCertificate(ctx, hostname, csrPEM, key)
	if err != nil {
		m.recordBackoff(hostname, err)
		return err
	}
	m.clearBackoff(hostname)
	if err := writeAtomic(m.keyPath, keyPEM, 0o400, m.fileUID, m.fileGID); err != nil {
		return fmt.Errorf("write ingress private key: %w", err)
	}
	if err := writeAtomic(m.certPath, certificatePEM, 0o400, m.fileUID, m.fileGID); err != nil {
		return fmt.Errorf("write ingress certificate: %w", err)
	}
	return nil
}

func (m *Manager) ensureInstalledFileAccess() error {
	if err := ensureOwnershipAndMode(m.keyPath, 0o400, m.fileUID, m.fileGID); err != nil {
		return err
	}
	if err := ensureOwnershipAndMode(m.certPath, 0o400, m.fileUID, m.fileGID); err != nil {
		return err
	}
	return nil
}

func (m *Manager) currentCertificateValid(hostname string) (bool, error) {
	certPEM, err := os.ReadFile(m.certPath)
	if err != nil {
		return false, err
	}
	keyPEM, err := os.ReadFile(m.keyPath)
	if err != nil {
		return false, err
	}
	cert, err := parseCertificatePEM(certPEM)
	if err != nil {
		return false, err
	}
	key, err := parseRSAPrivateKeyPEM(keyPEM)
	if err != nil {
		return false, err
	}
	if err := cert.VerifyHostname(hostname); err != nil {
		return false, err
	}
	if !cert.NotAfter.After(m.now().Add(m.renewBefore)) {
		return false, fmt.Errorf("certificate expires too soon: %s", cert.NotAfter.UTC().Format(time.RFC3339))
	}
	if !publicKeysEqual(cert.PublicKey, &key.PublicKey) {
		return false, errors.New("certificate does not match private key")
	}
	return true, nil
}

func (m *Manager) issueCertificate(ctx context.Context, hostname string, csrPEM []byte, key *rsa.PrivateKey) ([]byte, error) {
	token, _ := m.tokens.ControlPlaneAccessToken()
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("missing control plane access token")
	}

	payload, err := json.Marshal(map[string]string{
		"hostname": hostname,
		"csr":      string(csrPEM),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal ingress certificate request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+issuancePath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build ingress certificate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set(version.CapabilitiesHeader, version.CapabilityHeaderValue())

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request ingress certificate: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read ingress certificate response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, m.issuanceRequestError(resp, body)
	}

	var issued issuanceResponse
	if err := json.Unmarshal(body, &issued); err != nil {
		return nil, fmt.Errorf("decode ingress certificate response: %w", err)
	}
	if strings.TrimSpace(issued.CertificatePEM) == "" {
		return nil, errors.New("ingress certificate response missing certificate_pem")
	}
	if issued.Hostname != "" && !strings.EqualFold(strings.TrimSpace(issued.Hostname), hostname) {
		return nil, fmt.Errorf("ingress certificate hostname mismatch: got %s want %s", issued.Hostname, hostname)
	}

	certificatePEM := []byte(strings.TrimSpace(issued.CertificatePEM) + "\n")
	cert, err := parseCertificatePEM(certificatePEM)
	if err != nil {
		return nil, fmt.Errorf("parse issued ingress certificate: %w", err)
	}
	if err := cert.VerifyHostname(hostname); err != nil {
		return nil, fmt.Errorf("issued ingress certificate hostname invalid: %w", err)
	}
	if !publicKeysEqual(cert.PublicKey, &key.PublicKey) {
		return nil, errors.New("issued ingress certificate does not match generated private key")
	}
	return certificatePEM, nil
}

func (m *Manager) issuanceRequestError(resp *http.Response, body []byte) error {
	message := strings.TrimSpace(string(body))
	if decoded := parseIssuanceErrorResponse(body); decoded != "" {
		message = decoded
	}
	if message == "" {
		message = resp.Status
	}
	message = "ingress certificate request failed: " + message

	if retryAfter, ok := retryAfterTime(resp, m.now()); ok {
		return &retryAfterError{message: message, retryAfter: retryAfter}
	}
	return errors.New(message)
}

func parseIssuanceErrorResponse(body []byte) string {
	var payload issuanceErrorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if value := strings.TrimSpace(payload.ErrorDescription); value != "" {
		return value
	}
	if value := strings.TrimSpace(payload.Error); value != "" {
		return value
	}
	return ""
}

func retryAfterTime(resp *http.Response, now time.Time) (time.Time, bool) {
	value := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if value == "" {
		return time.Time{}, false
	}
	if seconds, err := time.ParseDuration(value + "s"); err == nil {
		return now.Add(seconds), true
	}
	if retryAfter, err := http.ParseTime(value); err == nil {
		return retryAfter, true
	}
	return time.Time{}, false
}

func (m *Manager) activeBackoff(hostname string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.backoffByHost[hostname]
	if !ok || !state.Until.After(m.now()) {
		return nil
	}
	return fmt.Errorf(
		"ingress certificate issuance backed off until %s: %s",
		state.Until.UTC().Format(time.RFC3339),
		state.Message,
	)
}

func (m *Manager) recordBackoff(hostname string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	state := m.backoffByHost[hostname]
	state.Attempts++
	state.Message = err.Error()
	if retryErr, ok := err.(*retryAfterError); ok && retryErr.retryAfter.After(now) {
		state.Until = retryErr.retryAfter
		m.backoffByHost[hostname] = state
		return
	}

	index := state.Attempts - 1
	if index >= len(issuanceBackoffSchedule) {
		index = len(issuanceBackoffSchedule) - 1
	}
	state.Until = now.Add(issuanceBackoffSchedule[index])
	m.backoffByHost[hostname] = state
}

func (m *Manager) clearBackoff(hostname string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.backoffByHost, hostname)
}

func generateCSR(hostname string) ([]byte, []byte, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, err
	}
	requestDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: hostname},
		DNSNames: []string{hostname},
	}, key)
	if err != nil {
		return nil, nil, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: requestDER,
	})
	return keyPEM, csrPEM, key, nil
}

func parseCertificatePEM(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("missing certificate pem block")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseRSAPrivateKeyPEM(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("missing private key pem block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not rsa")
	}
	return key, nil
}

func publicKeysEqual(left any, right *rsa.PublicKey) bool {
	lhs, ok := left.(*rsa.PublicKey)
	if !ok {
		return false
	}
	return lhs.N.Cmp(right.N) == 0 && lhs.E == right.E
}

func writeAtomic(path string, data []byte, mode os.FileMode, uid int, gid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	defer os.Remove(tmpPath)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return ensureOwnershipAndMode(path, mode, uid, gid)
}

func ensureOwnershipAndMode(path string, mode os.FileMode, uid int, gid int) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != mode {
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && int(stat.Uid) == uid && int(stat.Gid) == gid {
		return nil
	}
	return os.Chown(path, uid, gid)
}

func normalizedIngressMode(ingress *desiredstatepb.Ingress) string {
	if ingress == nil {
		return ""
	}
	mode := strings.TrimSpace(ingress.GetMode())
	if mode == "" {
		return "tunnel"
	}
	return mode
}
