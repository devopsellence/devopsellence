package origincert

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

type fakeTokenSource struct {
	token string
}

func (f fakeTokenSource) ControlPlaneAccessToken() (string, time.Time) {
	return f.token, time.Time{}
}

func TestEnsureRequestsAndWritesIngressCertificate(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != issuancePath {
			t.Fatalf("path = %s, want %s", r.URL.Path, issuancePath)
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		certPEM := signCSR(t, req["csr"], "abc123.devopsellence.io", time.Now().Add(365*24*time.Hour))
		_ = json.NewEncoder(w).Encode(map[string]string{
			"certificate_pem": string(certPEM),
			"hostname":        "abc123.devopsellence.io",
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	mgr, err := New(Config{
		BaseURL:     server.URL,
		CertPath:    filepath.Join(dir, "ingress.crt"),
		KeyPath:     filepath.Join(dir, "ingress.key"),
		FileUID:     os.Getuid(),
		FileGID:     os.Getgid(),
		RenewBefore: 24 * time.Hour,
		HTTPClient:  server.Client(),
		Tokens:      fakeTokenSource{token: "cp-access"},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := mgr.Ensure(context.Background(), &desiredstatepb.Ingress{
		Mode:     "direct_dns",
		Hostname: "abc123.devopsellence.io",
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 issuance call, got %d", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "ingress.crt")); err != nil {
		t.Fatalf("missing cert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ingress.key")); err != nil {
		t.Fatalf("missing key: %v", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "ingress.key"))
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if !strings.Contains(string(keyPEM), "BEGIN RSA PRIVATE KEY") {
		t.Fatalf("expected PKCS#1 private key, got %q", string(keyPEM))
	}
	assertFileModeAndOwner(t, filepath.Join(dir, "ingress.key"), 0o400, os.Getuid(), os.Getgid())
	assertFileModeAndOwner(t, filepath.Join(dir, "ingress.crt"), 0o400, os.Getuid(), os.Getgid())
}

func TestEnsureSkipsRenewalWhileCertificateRemainsValid(t *testing.T) {
	dir := t.TempDir()
	if err := writeIssuedCertificate(filepath.Join(dir, "ingress.crt"), filepath.Join(dir, "ingress.key"), "abc123.devopsellence.io", time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("write issued certificate: %v", err)
	}

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	mgr, err := New(Config{
		BaseURL:     server.URL,
		CertPath:    filepath.Join(dir, "ingress.crt"),
		KeyPath:     filepath.Join(dir, "ingress.key"),
		FileUID:     os.Getuid(),
		FileGID:     os.Getgid(),
		RenewBefore: 24 * time.Hour,
		HTTPClient:  server.Client(),
		Tokens:      fakeTokenSource{token: "cp-access"},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := mgr.Ensure(context.Background(), &desiredstatepb.Ingress{
		Mode:     "direct_dns",
		Hostname: "abc123.devopsellence.io",
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected no issuance call, got %d", calls)
	}
	assertFileModeAndOwner(t, filepath.Join(dir, "ingress.key"), 0o400, os.Getuid(), os.Getgid())
	assertFileModeAndOwner(t, filepath.Join(dir, "ingress.crt"), 0o400, os.Getuid(), os.Getgid())
}

func TestEnsureRenewsWhenCertificateNearExpiry(t *testing.T) {
	dir := t.TempDir()
	if err := writeIssuedCertificate(filepath.Join(dir, "ingress.crt"), filepath.Join(dir, "ingress.key"), "abc123.devopsellence.io", time.Now().Add(2*time.Hour)); err != nil {
		t.Fatalf("write issued certificate: %v", err)
	}

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		certPEM := signCSR(t, req["csr"], "abc123.devopsellence.io", time.Now().Add(365*24*time.Hour))
		_ = json.NewEncoder(w).Encode(map[string]string{
			"certificate_pem": string(certPEM),
			"hostname":        "abc123.devopsellence.io",
		})
	}))
	defer server.Close()

	mgr, err := New(Config{
		BaseURL:     server.URL,
		CertPath:    filepath.Join(dir, "ingress.crt"),
		KeyPath:     filepath.Join(dir, "ingress.key"),
		FileUID:     os.Getuid(),
		FileGID:     os.Getgid(),
		RenewBefore: 24 * time.Hour,
		HTTPClient:  server.Client(),
		Tokens:      fakeTokenSource{token: "cp-access"},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := mgr.Ensure(context.Background(), &desiredstatepb.Ingress{
		Mode:     "direct_dns",
		Hostname: "abc123.devopsellence.io",
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 issuance call, got %d", calls)
	}
	assertFileModeAndOwner(t, filepath.Join(dir, "ingress.key"), 0o400, os.Getuid(), os.Getgid())
	assertFileModeAndOwner(t, filepath.Join(dir, "ingress.crt"), 0o400, os.Getuid(), os.Getgid())
}

func TestEnsureBacksOffAfterGenericIssuanceFailure(t *testing.T) {
	now := time.Date(2026, 4, 1, 18, 57, 0, 0, time.UTC)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"error":"server_error","error_description":"boom"}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()

	dir := t.TempDir()
	mgr, err := New(Config{
		BaseURL:     server.URL,
		CertPath:    filepath.Join(dir, "ingress.crt"),
		KeyPath:     filepath.Join(dir, "ingress.key"),
		FileUID:     os.Getuid(),
		FileGID:     os.Getgid(),
		RenewBefore: 24 * time.Hour,
		HTTPClient:  server.Client(),
		Tokens:      fakeTokenSource{token: "cp-access"},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	mgr.now = func() time.Time { return now }

	ingress := &desiredstatepb.Ingress{
		Mode:     "direct_dns",
		Hostname: "abc123.devopsellence.io",
	}
	if err := mgr.Ensure(context.Background(), ingress); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected issuance error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 issuance call, got %d", calls)
	}

	if err := mgr.Ensure(context.Background(), ingress); err == nil || !strings.Contains(err.Error(), "backed off until 2026-04-01T18:57:15Z") {
		t.Fatalf("expected backoff error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected no extra issuance call during backoff, got %d", calls)
	}

	now = now.Add(16 * time.Second)
	if err := mgr.Ensure(context.Background(), ingress); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected issuance retry error, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected second issuance call after backoff, got %d", calls)
	}
}

func TestNewUsesLongEnoughDefaultHTTPTimeoutForIssuance(t *testing.T) {
	dir := t.TempDir()
	mgr, err := New(Config{
		BaseURL:     "https://cp.devopsellence.test",
		CertPath:    filepath.Join(dir, "ingress.crt"),
		KeyPath:     filepath.Join(dir, "ingress.key"),
		FileUID:     os.Getuid(),
		FileGID:     os.Getgid(),
		RenewBefore: 24 * time.Hour,
		Tokens:      fakeTokenSource{token: "cp-access"},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if got, want := mgr.httpClient.Timeout, defaultHTTPTimeout; got != want {
		t.Fatalf("default timeout = %s, want %s", got, want)
	}
}

func TestEnsureHonorsRetryAfterHeader(t *testing.T) {
	now := time.Date(2026, 4, 1, 18, 57, 0, 0, time.UTC)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "73")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate_limited","error_description":"retry later"}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	mgr, err := New(Config{
		BaseURL:     server.URL,
		CertPath:    filepath.Join(dir, "ingress.crt"),
		KeyPath:     filepath.Join(dir, "ingress.key"),
		FileUID:     os.Getuid(),
		FileGID:     os.Getgid(),
		RenewBefore: 24 * time.Hour,
		HTTPClient:  server.Client(),
		Tokens:      fakeTokenSource{token: "cp-access"},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	mgr.now = func() time.Time { return now }

	ingress := &desiredstatepb.Ingress{
		Mode:     "direct_dns",
		Hostname: "abc123.devopsellence.io",
	}
	if err := mgr.Ensure(context.Background(), ingress); err == nil || !strings.Contains(err.Error(), "retry later") {
		t.Fatalf("expected issuance error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 issuance call, got %d", calls)
	}

	if err := mgr.Ensure(context.Background(), ingress); err == nil || !strings.Contains(err.Error(), "backed off until 2026-04-01T18:58:13Z") {
		t.Fatalf("expected retry-after backoff error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected no extra issuance call during retry-after backoff, got %d", calls)
	}

	now = now.Add(74 * time.Second)
	if err := mgr.Ensure(context.Background(), ingress); err == nil || !strings.Contains(err.Error(), "retry later") {
		t.Fatalf("expected issuance retry error, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected second issuance call after retry-after, got %d", calls)
	}
}

func signCSR(t *testing.T, csrPEM string, hostname string, notAfter time.Time) []byte {
	t.Helper()
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		t.Fatal("missing csr pem block")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatalf("parse csr: %v", err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatalf("check csr signature: %v", err)
	}

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-origin-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: hostname},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		DNSNames:     []string{hostname},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, template, caCert, csr.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
}

func writeIssuedCertificate(certPath string, keyPath string, hostname string, notAfter time.Time) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: hostname},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		DNSNames:     []string{hostname},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return err
	}
	return nil
}

func assertFileModeAndOwner(t *testing.T, path string, mode os.FileMode, uid int, gid int) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("mode for %s = %#o, want %#o", path, info.Mode().Perm(), mode)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("missing unix stat for %s", path)
	}
	if int(stat.Uid) != uid || int(stat.Gid) != gid {
		t.Fatalf("owner for %s = %d:%d, want %d:%d", path, stat.Uid, stat.Gid, uid, gid)
	}
}
