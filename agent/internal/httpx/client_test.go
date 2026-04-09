package httpx

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"
)

func TestNewClientAppliesDefaults(t *testing.T) {
	client := NewClient(15 * time.Second)
	if client.Timeout != 15*time.Second {
		t.Fatalf("expected timeout 15s, got %s", client.Timeout)
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if transport.MaxIdleConns != 64 {
		t.Fatalf("expected MaxIdleConns 64, got %d", transport.MaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != 8 {
		t.Fatalf("expected MaxIdleConnsPerHost 8, got %d", transport.MaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("expected IdleConnTimeout 90s, got %s", transport.IdleConnTimeout)
	}
	if transport.TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("expected TLSHandshakeTimeout 10s, got %s", transport.TLSHandshakeTimeout)
	}
	if transport.ExpectContinueTimeout != time.Second {
		t.Fatalf("expected ExpectContinueTimeout 1s, got %s", transport.ExpectContinueTimeout)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("expected TLSClientConfig to be set")
	}
	if transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("expected MinVersion TLS1.2, got %d", transport.TLSClientConfig.MinVersion)
	}
}

func TestNewClientAllowsZeroTimeout(t *testing.T) {
	client := NewClient(0)
	if client.Timeout != 0 {
		t.Fatalf("expected zero timeout, got %s", client.Timeout)
	}
}
