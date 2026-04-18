package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/report"
	"github.com/devopsellence/devopsellence/agent/internal/version"
)

type staticTokens struct{}

func (staticTokens) ControlPlaneAccessToken() (string, time.Time) {
	return "node-access-token", time.Now().Add(time.Hour)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func TestReporterPostsStatusToControlPlane(t *testing.T) {
	t.Parallel()

	var captured report.Status
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			if r.URL.Path != statusPath {
				t.Fatalf("path = %s, want %s", r.URL.Path, statusPath)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer node-access-token" {
				t.Fatalf("authorization = %q", got)
			}
			if got := r.Header.Get(version.CapabilitiesHeader); got != version.CapabilityHeaderValue() {
				t.Fatalf("capabilities = %q", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"tracked":true}`)),
			}, nil
		}),
	}

	reporter, err := New(Config{
		BaseURL:    "https://control-plane.test",
		HTTPClient: httpClient,
		Tokens:     staticTokens{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	status := report.Status{
		Time:     time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
		Revision: "rel-1",
		Phase:    report.PhaseReconciling,
		Message:  "pulling image",
		Summary: &report.Summary{
			Environments: 1,
			Services:     1,
		},
		Environments: []report.EnvironmentStatus{{
			Name:     "production",
			Revision: "rel-1",
			Phase:    report.PhaseReconciling,
			Services: []report.ServiceStatus{{
				Name:  "web",
				Kind:  "web",
				Phase: report.PhaseReconciling,
				State: "starting",
				Hash:  "hash-1",
			}},
		}},
		Containers: []report.ContainerStatus{
			{Name: "web", State: "starting", Hash: "hash-1"},
		},
	}
	if err := reporter.Report(context.Background(), status); err != nil {
		t.Fatalf("Report() error = %v", err)
	}

	if captured.Revision != status.Revision {
		t.Fatalf("revision = %s, want %s", captured.Revision, status.Revision)
	}
	if captured.Phase != status.Phase {
		t.Fatalf("phase = %s, want %s", captured.Phase, status.Phase)
	}
	if len(captured.Containers) != 1 || captured.Containers[0].State != "starting" {
		t.Fatalf("containers = %#v", captured.Containers)
	}
	if captured.Summary == nil || captured.Summary.Services != 1 {
		t.Fatalf("summary = %#v", captured.Summary)
	}
	if len(captured.Environments) != 1 || captured.Environments[0].Services[0].State != "starting" {
		t.Fatalf("environments = %#v", captured.Environments)
	}
}
