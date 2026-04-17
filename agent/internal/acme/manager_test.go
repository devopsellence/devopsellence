package acme

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTP01ProviderForwardsChallengeMissToPeer(t *testing.T) {
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
	req.Header.Set(peerHeader, "1")
	rec := httptest.NewRecorder()
	provider.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPeerChallengeURL(t *testing.T) {
	got := peerChallengeURL("203.0.113.10", "/.well-known/acme-challenge/token-a")
	if got != "http://203.0.113.10/.well-known/acme-challenge/token-a" {
		t.Fatalf("url = %q", got)
	}

	got = peerChallengeURL("2001:db8::1", "/.well-known/acme-challenge/token-a")
	if got != "http://[2001:db8::1]/.well-known/acme-challenge/token-a" {
		t.Fatalf("ipv6 url = %q", got)
	}
}
