package solo

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/config"
)

func TestBuildDesiredState_WebOnly(t *testing.T) {
	cfg := &config.ProjectConfig{
		Project: "myapp",
		Web: config.ServiceConfig{
			Command: "rails server",
			Port:    3000,
			Env:     map[string]string{"RAILS_ENV": "production"},
			SecretRefs: []config.SecretRef{
				{Name: "DATABASE_URL", Secret: "projects/x/secrets/db"},
			},
			Healthcheck: &config.HTTPHealthcheck{Path: "/up", Port: 3000},
		},
	}

	secrets := map[string]string{"DATABASE_URL": "postgres://localhost/mydb"}

	data, err := BuildDesiredState(cfg, "myapp:abc1234", "abc1234", secrets)
	if err != nil {
		t.Fatal(err)
	}

	var ds desiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}

	if ds.Revision != "abc1234" {
		t.Errorf("expected revision abc1234, got %s", ds.Revision)
	}
	if len(ds.Environments) != 1 {
		t.Fatalf("expected 1 environment, got %d", len(ds.Environments))
	}
	environment := ds.Environments[0]
	if environment.Name != config.DefaultEnvironment {
		t.Fatalf("expected default environment, got %s", environment.Name)
	}
	if len(environment.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(environment.Services))
	}

	web := environment.Services[0]
	if web.Name != "web" {
		t.Errorf("expected service name web, got %s", web.Name)
	}
	if web.Kind != "web" {
		t.Errorf("expected service kind web, got %s", web.Kind)
	}
	if web.Image != "myapp:abc1234" {
		t.Errorf("expected image myapp:abc1234, got %s", web.Image)
	}
	if web.Env["RAILS_ENV"] != "production" {
		t.Errorf("expected RAILS_ENV=production")
	}
	if web.Env["DATABASE_URL"] != "postgres://localhost/mydb" {
		t.Errorf("expected DATABASE_URL resolved, got %s", web.Env["DATABASE_URL"])
	}
	if web.Healthcheck == nil || web.Healthcheck.Path != "/up" {
		t.Errorf("expected healthcheck /up")
	}
	if web.Healthcheck.IntervalSeconds != 5 || web.Healthcheck.TimeoutSeconds != 2 || web.Healthcheck.Retries != 3 || web.Healthcheck.StartPeriodSeconds != 1 {
		t.Errorf("healthcheck timing = %#v, want control-plane defaults", web.Healthcheck)
	}
	if len(web.Ports) != 1 || web.Ports[0].Name != "http" || web.Ports[0].Port != 3000 {
		t.Errorf("expected http port 3000, got %#v", web.Ports)
	}

	// No secret_refs in output.
	rawData := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &rawData); err != nil {
		t.Fatal(err)
	}
	for _, c := range environment.Services {
		// Verify command is shell wrapped.
		if len(c.Command) != 3 || c.Command[0] != "sh" {
			t.Errorf("expected shell-wrapped command, got %v", c.Command)
		}
	}
}

func TestBuildDesiredState_WithWorkerAndReleaseCommand(t *testing.T) {
	cfg := &config.ProjectConfig{
		Project: "myapp",
		Web: config.ServiceConfig{
			Port:        3000,
			Env:         map[string]string{},
			SecretRefs:  []config.SecretRef{},
			Healthcheck: &config.HTTPHealthcheck{Path: "/", Port: 3000},
		},
		Worker: &config.ServiceConfig{
			Command:    "sidekiq",
			Env:        map[string]string{"QUEUE": "default"},
			SecretRefs: []config.SecretRef{},
		},
		ReleaseCommand: "rails db:migrate",
	}

	data, err := BuildDesiredState(cfg, "myapp:def5678", "def5678", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	var ds desiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}

	environment := ds.Environments[0]
	if len(environment.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(environment.Services))
	}

	if environment.Services[1].Name != "worker" {
		t.Errorf("expected worker service, got %s", environment.Services[1].Name)
	}

	if len(environment.Tasks) != 1 {
		t.Fatal("expected release command")
	}
	releaseCommand := environment.Tasks[0]
	if releaseCommand.Name != "release" {
		t.Errorf("expected name 'release', got %s", releaseCommand.Name)
	}
	if releaseCommand.Image != "myapp:def5678" {
		t.Errorf("expected image myapp:def5678, got %s", releaseCommand.Image)
	}
}

func TestBuildDesiredStateForLabelsFiltersServices(t *testing.T) {
	cfg := &config.ProjectConfig{
		Project: "myapp",
		Web: config.ServiceConfig{
			Port:        3000,
			Env:         map[string]string{},
			SecretRefs:  []config.SecretRef{},
			Healthcheck: &config.HTTPHealthcheck{Path: "/", Port: 3000},
		},
		Worker: &config.ServiceConfig{
			Command:    "sidekiq",
			Env:        map[string]string{},
			SecretRefs: []config.SecretRef{},
		},
		ReleaseCommand: "rails db:migrate",
	}

	data, err := BuildDesiredStateForLabels(cfg, "myapp:def5678", "def5678", map[string]string{}, []string{config.NodeLabelWorker}, false)
	if err != nil {
		t.Fatal(err)
	}
	var ds desiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	environment := ds.Environments[0]
	if len(environment.Services) != 1 || environment.Services[0].Name != "worker" {
		t.Fatalf("services = %#v, want worker only", environment.Services)
	}
	if len(environment.Tasks) != 0 {
		t.Fatal("release command should not be scheduled on worker-only node")
	}
}

func TestBuildDesiredStateForLabelsIncludesReleaseWhenSelected(t *testing.T) {
	cfg := &config.ProjectConfig{
		Project: "myapp",
		Web: config.ServiceConfig{
			Port:        3000,
			Env:         map[string]string{},
			SecretRefs:  []config.SecretRef{},
			Healthcheck: &config.HTTPHealthcheck{Path: "/", Port: 3000},
		},
		ReleaseCommand: "rails db:migrate",
	}

	data, err := BuildDesiredStateForLabels(cfg, "myapp:def5678", "def5678", map[string]string{}, []string{config.NodeLabelWeb}, true)
	if err != nil {
		t.Fatal(err)
	}
	var ds desiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	environment := ds.Environments[0]
	if len(environment.Services) != 1 || environment.Services[0].Name != "web" {
		t.Fatalf("services = %#v, want web only", environment.Services)
	}
	if len(environment.Tasks) != 1 {
		t.Fatal("expected release command")
	}
}

func TestBuildDesiredStateForNodeIncludesIngressForPublicWebNode(t *testing.T) {
	cfg := &config.ProjectConfig{
		Project: "myapp",
		Web: config.ServiceConfig{
			Port:        3000,
			Env:         map[string]string{},
			SecretRefs:  []config.SecretRef{},
			Healthcheck: &config.HTTPHealthcheck{Path: "/", Port: 3000},
		},
		Ingress: &config.IngressConfig{
			Hosts: []string{"app.example.com", "www.example.com"},
			TLS: config.IngressTLSConfig{
				Mode:           "auto",
				Email:          "ops@example.com",
				CADirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
			},
			RedirectHTTP: true,
		},
	}

	data, err := BuildDesiredStateForNode(cfg, "myapp:def5678", "def5678", map[string]string{}, []string{config.NodeLabelWeb}, true, false, []NodePeer{{
		Name:          "web-b",
		Labels:        []string{config.NodeLabelWeb},
		PublicAddress: "203.0.113.11",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var ds desiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	if ds.Ingress == nil {
		t.Fatal("expected ingress")
	}
	if strings.Join(ds.Ingress.Hosts, ",") != "app.example.com,www.example.com" {
		t.Fatalf("hosts = %#v", ds.Ingress.Hosts)
	}
	if ds.Ingress.Mode != "public" || ds.Ingress.TLS.Mode != "auto" || ds.Ingress.TLS.Email != "ops@example.com" || !ds.Ingress.RedirectHTTP {
		t.Fatalf("ingress = %#v", ds.Ingress)
	}
	if len(ds.Ingress.Routes) != 2 || ds.Ingress.Routes[0].Target.Environment != config.DefaultEnvironment || ds.Ingress.Routes[0].Target.Service != "web" {
		t.Fatalf("routes = %#v", ds.Ingress.Routes)
	}
	if len(ds.NodePeers) != 1 || ds.NodePeers[0].Name != "web-b" || ds.NodePeers[0].PublicAddress != "203.0.113.11" {
		t.Fatalf("node peers = %#v", ds.NodePeers)
	}
}

func TestBuildDesiredStateForNodeOmitsIngressForWorkerNode(t *testing.T) {
	cfg := &config.ProjectConfig{
		Project: "myapp",
		Web: config.ServiceConfig{
			Port:        3000,
			Env:         map[string]string{},
			SecretRefs:  []config.SecretRef{},
			Healthcheck: &config.HTTPHealthcheck{Path: "/", Port: 3000},
		},
		Worker:  &config.ServiceConfig{Command: "sidekiq"},
		Ingress: &config.IngressConfig{Hosts: []string{"app.example.com"}},
	}

	data, err := BuildDesiredStateForNode(cfg, "myapp:def5678", "def5678", map[string]string{}, []string{config.NodeLabelWorker}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var ds desiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	if ds.Ingress != nil {
		t.Fatalf("ingress = %#v, want nil", ds.Ingress)
	}
}

func TestBuildDesiredState_MissingSecretErrors(t *testing.T) {
	cfg := &config.ProjectConfig{
		Project: "myapp",
		Web: config.ServiceConfig{
			Port:        3000,
			Env:         map[string]string{},
			SecretRefs:  []config.SecretRef{{Name: "DATABASE_URL", Secret: "projects/x/secrets/db"}},
			Healthcheck: &config.HTTPHealthcheck{Path: "/", Port: 3000},
		},
	}

	// No secrets provided — should fail.
	_, err := BuildDesiredState(cfg, "myapp:abc1234", "abc1234", map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing secret, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "DATABASE_URL") {
		t.Errorf("expected error to mention DATABASE_URL, got: %s", got)
	}
}
