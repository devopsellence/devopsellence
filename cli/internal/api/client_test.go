package api

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

func TestClientSetsUserAgent(t *testing.T) {
	client := New("https://dev.devopsellence.test")
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got, want := req.Header.Get("User-Agent"), version.UserAgent(); got != want {
			t.Fatalf("User-Agent = %q, want %q", got, want)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"organizations":[]}`)),
		}, nil
	})}

	if _, err := client.ListOrganizations(context.Background(), "token"); err != nil {
		t.Fatalf("ListOrganizations() error = %v", err)
	}
}
