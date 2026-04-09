package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/version"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestManagerPostSetsUserAgent(t *testing.T) {
	manager := New(nil, "https://dev.devopsellence.test", "https://dev.devopsellence.test")
	manager.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got, want := req.Header.Get("User-Agent"), version.UserAgent(); got != want {
			t.Fatalf("User-Agent = %q, want %q", got, want)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":3600}`)),
		}, nil
	})}

	if _, err := manager.post(context.Background(), manager.APIBase+"/api/v1/cli/auth/token", map[string]any{"code": "test"}); err != nil {
		t.Fatalf("post() error = %v", err)
	}
}
