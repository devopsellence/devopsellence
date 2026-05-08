package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

func TestHTTP01ProviderForwardsChallengeMissToNodePeer(t *testing.T) {
	peer := NewHTTP01Provider()
	if err := peer.Present("app.example.com", "token-a", "key-auth-a"); err != nil {
		t.Fatal(err)
	}
	peerServer := httptest.NewServer(peer)
	defer peerServer.Close()

	provider := NewHTTP01Provider()
	provider.SetPeers([]string{peerServer.URL})

	req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/token-a", nil)
	rec := httptest.NewRecorder()
	provider.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "key-auth-a" {
		t.Fatalf("body = %q, want key-auth-a", rec.Body.String())
	}
}

func TestHTTP01ProviderDoesNotForwardPeerRequests(t *testing.T) {
	provider := NewHTTP01Provider()
	provider.SetPeers([]string{"http://127.0.0.1:1"})

	req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/missing", nil)
	req.Header.Set(nodePeerHeader, "1")
	rec := httptest.NewRecorder()
	provider.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestNodePeerPublicWebAddresses(t *testing.T) {
	got := nodePeerPublicWebAddresses([]*desiredstatepb.NodePeer{
		{Name: "web-a", Labels: []string{"web"}, PublicAddress: "203.0.113.10"},
		{Name: "web-b", Labels: []string{"web"}, PublicAddress: "203.0.113.10"},
		{Name: "worker-a", Labels: []string{"worker"}, PublicAddress: "203.0.113.11"},
		{Name: "unlabeled", PublicAddress: "203.0.113.12"},
	})
	if len(got) != 1 || got[0] != "203.0.113.10" {
		t.Fatalf("addresses = %#v, want 203.0.113.10", got)
	}
}

func TestPeerChallengeURL(t *testing.T) {
	got := peerChallengeURL("203.0.113.10", "/.well-known/acme-challenge/token-a")
	if got != "http://203.0.113.10/.well-known/acme-challenge/token-a" {
		t.Fatalf("url = %q, want http://203.0.113.10/.well-known/acme-challenge/token-a", got)
	}

	got = peerChallengeURL("2001:db8::1", "/.well-known/acme-challenge/token-a")
	if got != "http://[2001:db8::1]/.well-known/acme-challenge/token-a" {
		t.Fatalf("ipv6 url = %q", got)
	}
}

func TestNeedsAutoTLSTreatsBlankModeAsPublic(t *testing.T) {
	ingress := &desiredstatepb.Ingress{Hosts: []string{"app.example.com"}}
	if !needsAutoTLS(ingress) {
		t.Fatal("expected blank mode ingress to need auto TLS")
	}
}

func TestStatusReportsPendingUntilCertificateExists(t *testing.T) {
	manager := New(Config{CertPath: filepath.Join(t.TempDir(), "cert.pem")})

	status := manager.Status(&desiredstatepb.Ingress{
		Mode:  "public",
		Hosts: []string{"app.example.com"},
		Tls:   &desiredstatepb.IngressTLS{Mode: "auto"},
	})

	if status == nil || status.TLSStatus != "pending" {
		t.Fatalf("status = %#v, want pending", status)
	}
}

func TestStatusReportsReadyForMatchingCertificate(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	notAfter := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	writeTestCertificate(t, certPath, []string{"app.example.com"}, notAfter)

	manager := New(Config{CertPath: certPath})
	status := manager.Status(&desiredstatepb.Ingress{
		Mode:  "public",
		Hosts: []string{"app.example.com"},
		Tls:   &desiredstatepb.IngressTLS{Mode: "auto"},
	})

	if status == nil || status.TLSStatus != "ready" {
		t.Fatalf("status = %#v, want ready", status)
	}
	if status.TLSNotAfter == nil || !status.TLSNotAfter.Equal(notAfter) {
		t.Fatalf("not_after = %#v, want %s", status.TLSNotAfter, notAfter)
	}
}

func TestStatusReportsLastAutoTLSErrorWhenCertificateMissing(t *testing.T) {
	manager := New(Config{CertPath: filepath.Join(t.TempDir(), "cert.pem")})
	manager.recordEnsureResult(os.ErrPermission)

	status := manager.Status(&desiredstatepb.Ingress{
		Mode:  "public",
		Hosts: []string{"app.example.com"},
		Tls:   &desiredstatepb.IngressTLS{Mode: "auto"},
	})

	if status == nil || status.TLSStatus != "failed" || status.TLSError == "" {
		t.Fatalf("status = %#v, want failed with error", status)
	}
}

func TestStatusReportsCertificateParseError(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(certPath, []byte("not a certificate"), 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	manager := New(Config{CertPath: certPath})
	status := manager.Status(&desiredstatepb.Ingress{
		Mode:  "public",
		Hosts: []string{"app.example.com"},
		Tls:   &desiredstatepb.IngressTLS{Mode: "auto"},
	})

	if status == nil || status.TLSStatus != "failed" || status.TLSError == "" {
		t.Fatalf("status = %#v, want failed with certificate error", status)
	}
}

func writeTestCertificate(t *testing.T, path string, hosts []string, notAfter time.Time) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: hosts[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		DNSNames:     hosts,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
}
