package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devopsellence/cli/internal/agentsmd"
	"github.com/devopsellence/cli/internal/api"
	"github.com/devopsellence/cli/internal/auth"
	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/discovery"
	"github.com/devopsellence/cli/internal/docker"
	"github.com/devopsellence/cli/internal/git"
	"github.com/devopsellence/cli/internal/output"
	"github.com/devopsellence/cli/internal/state"
)

func TestInitWritesAgentsFile(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.Init(context.Background(), InitOptions{NonInteractive: true}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, agentsmd.FilePath))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "devopsellence mode use solo|shared") {
		t.Fatalf("AGENTS.md = %q, want mode guidance", text)
	}
	if !strings.Contains(text, "devopsellence secret set NAME") {
		t.Fatalf("AGENTS.md = %q, want secret guidance", text)
	}
	if !strings.Contains(text, "tasks.release") {
		t.Fatalf("AGENTS.md = %q, want lifecycle hook guidance", text)
	}
	if !strings.Contains(text, "Environment: production") {
		t.Fatalf("AGENTS.md = %q, want workspace defaults", text)
	}
}

func TestInitUpdatesManagedAgentsBlockOnly(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	path := filepath.Join(root, agentsmd.FilePath)
	existing := strings.Join([]string{
		"# Team Conventions",
		"",
		"Preserve this note.",
		"",
		"<!-- devopsellence:begin -->",
		"stale block",
		"<!-- devopsellence:end -->",
		"",
		"Footer note.",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed AGENTS.md: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 99, "name": "staging"}}}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.Init(context.Background(), InitOptions{Environment: "staging", NonInteractive: true}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "stale block") {
		t.Fatalf("AGENTS.md = %q, stale managed block still present", text)
	}
	if !strings.Contains(text, "Preserve this note.") || !strings.Contains(text, "Footer note.") {
		t.Fatalf("AGENTS.md = %q, custom content not preserved", text)
	}
	if !strings.Contains(text, "Environment: staging") {
		t.Fatalf("AGENTS.md = %q, want refreshed environment", text)
	}
	if !strings.Contains(text, "devopsellence doctor") {
		t.Fatalf("AGENTS.md = %q, want doctor guidance", text)
	}
}

func TestInitWritesGenericConfigAtRepoRoot(t *testing.T) {
	t.Parallel()

	root := makeGenericRoot(t)
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 11, "name": filepath.Base(root)}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 44, "name": "production"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.Init(context.Background(), InitOptions{NonInteractive: true}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, config.GenericFilePath)); err != nil {
		t.Fatalf("generic config missing: %v", err)
	}
	loaded, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded == nil || loaded.App.Type != config.AppTypeGeneric {
		t.Fatalf("loaded generic config mismatch: %#v", loaded)
	}
	web := webService(t, loaded)
	if web.Healthcheck == nil || web.Healthcheck.Path != "/" {
		t.Fatalf("generic healthcheck path = %#v, want /", web.Healthcheck)
	}
}

func TestInitBootstrapsSharedIngressFromCanonicalDomain(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	app := newTestAppWithDeployTarget(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/deploy_target":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"organization":         map[string]any{"id": 7, "name": "default"},
				"organization_created": false,
				"project":              map[string]any{"id": 11, "name": "ShopApp", "organization_id": 7},
				"project_created":      false,
				"environment": map[string]any{
					"id":            44,
					"name":          "production",
					"project_id":    11,
					"runtime_kind":  "managed",
					"ingress_hosts": []string{"www.prod-abc.devopsellence.io", "prod-abc.devopsellence.io"},
				},
				"environment_created": true,
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.Init(context.Background(), InitOptions{NonInteractive: true}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	loaded, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded == nil || loaded.Ingress == nil {
		t.Fatalf("expected bootstrapped ingress, got %#v", loaded)
	}
	if got, want := loaded.Ingress.Service, config.DefaultWebServiceName; got != want {
		t.Fatalf("ingress.service = %q, want %q", got, want)
	}
	if got, want := loaded.Ingress.Hosts, []string{"www.prod-abc.devopsellence.io", "prod-abc.devopsellence.io"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ingress.hosts = %#v, want %#v", got, want)
	}
	if got, want := loaded.Ingress.TLS.Mode, "off"; got != want {
		t.Fatalf("ingress.tls.mode = %q, want %q", got, want)
	}
	if loaded.Ingress.RedirectHTTP == nil {
		t.Fatal("expected explicit ingress.redirect_http=false")
	}
	if *loaded.Ingress.RedirectHTTP {
		t.Fatal("ingress.redirect_http = true, want false")
	}
}

func TestInitLeavesSharedIngressUnsetUntilCanonicalDomainExists(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	app := newTestAppWithDeployTarget(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/deploy_target":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"organization":         map[string]any{"id": 7, "name": "default"},
				"organization_created": false,
				"project":              map[string]any{"id": 11, "name": "ShopApp", "organization_id": 7},
				"project_created":      false,
				"environment": map[string]any{
					"id":           44,
					"name":         "production",
					"project_id":   11,
					"runtime_kind": "managed",
				},
				"environment_created": true,
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.Init(context.Background(), InitOptions{NonInteractive: true}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	loaded, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("expected config to load")
	}
	if loaded.Ingress != nil {
		t.Fatalf("expected shared ingress to stay unset without canonical host, got %#v", loaded.Ingress)
	}
}

func TestContextShowJSONIncludesWorkspaceContext(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))
	if err := app.WorkspaceState.Write(map[string]any{
		"modes": map[string]any{
			root: "shared",
		},
		"environments": map[string]any{
			root: "production",
		},
	}); err != nil {
		t.Fatalf("write workspace state: %v", err)
	}

	var stdout bytes.Buffer
	app.Printer.Out = &stdout
	app.Printer.JSON = true
	app.Printer.Interactive = false

	if err := app.ContextShow(); err != nil {
		t.Fatalf("ContextShow() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal whoami output: %v", err)
	}
	if payload["mode"] != "shared" {
		t.Fatalf("mode = %v, want shared", payload["mode"])
	}
	if stringValueAny(payload["organization"]) != "default" || stringValueAny(payload["project"]) != "ShopApp" {
		t.Fatalf("payload = %#v", payload)
	}
	if stringValueAny(payload["default_environment"]) != "staging" || stringValueAny(payload["selected_environment"]) != "production" || stringValueAny(payload["environment"]) != "production" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestOrganizationUseUpdatesConfig(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{
				{"id": 7, "name": "default", "role": "owner"},
				{"id": 8, "name": "acme", "role": "owner"},
			}}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.OrganizationUse(context.Background(), OrganizationUseOptions{Name: "acme"}); err != nil {
		t.Fatalf("OrganizationUse() error = %v", err)
	}

	loaded, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded.Organization != "acme" {
		t.Fatalf("organization = %q, want acme", loaded.Organization)
	}
}

func TestProjectUseUpdatesConfig(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{
				{"id": 11, "name": "ShopApp"},
				{"id": 12, "name": "Billing"},
			}}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.ProjectUse(context.Background(), ProjectUseOptions{Name: "Billing"}); err != nil {
		t.Fatalf("ProjectUse() error = %v", err)
	}

	loaded, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded.Project != "Billing" {
		t.Fatalf("project = %q, want Billing", loaded.Project)
	}
}

func TestEnvironmentUseUpdatesWorkspaceStateNotConfig(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{
				{"id": 44, "name": "staging"},
				{"id": 45, "name": "production"},
			}}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.EnvironmentUse(context.Background(), EnvironmentUseOptions{Name: "production"}); err != nil {
		t.Fatalf("EnvironmentUse() error = %v", err)
	}

	loaded, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded.DefaultEnvironment != "staging" {
		t.Fatalf("default_environment = %q, want staging", loaded.DefaultEnvironment)
	}
	state, err := app.WorkspaceState.Read()
	if err != nil {
		t.Fatalf("Read() workspace state error = %v", err)
	}
	environments, _ := state["environments"].(map[string]any)
	if stringValueAny(environments[root]) != "production" {
		t.Fatalf("workspace environments = %#v", environments)
	}
}

func TestEnvironmentOpenUsesWorkspaceContext(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "staging"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/status":
			return jsonResponse(t, map[string]any{
				"organization": map[string]any{"id": 7, "name": "default"},
				"project":      map[string]any{"id": 11, "name": "ShopApp"},
				"environment":  map[string]any{"id": 44, "name": "staging"},
				"ingress":      map[string]any{"public_url": "https://shop.example.test"},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	var opened string
	app.Auth.OpenURL = func(value string) error {
		opened = value
		return nil
	}

	if err := app.EnvironmentOpen(context.Background(), EnvironmentOpenOptions{}); err != nil {
		t.Fatalf("EnvironmentOpen() error = %v", err)
	}
	if opened != "https://shop.example.test" {
		t.Fatalf("opened = %q, want https://shop.example.test", opened)
	}
}

func TestConfigResolvePrintsResolvedEnvironmentConfig(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	project := config.DefaultProjectConfig("default", "ShopApp", "production")
	project.Ingress = &config.IngressConfig{
		Hosts:   []string{"app.example.test"},
		Service: "web",
	}
	project.Environments = map[string]config.EnvironmentOverlay{
		"staging": {
			Ingress: &config.IngressConfigOverlay{
				Hosts: []string{"staging.example.test"},
			},
			Services: map[string]config.ServiceConfigOverlay{
				"web": {
					Env: map[string]string{"RAILS_ENV": "staging"},
				},
			},
		},
	}
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))

	var stdout bytes.Buffer
	app.Printer.Out = &stdout
	app.Printer.JSON = true
	app.Printer.Interactive = false

	if err := app.ConfigResolve(ConfigResolveOptions{Environment: "staging"}); err != nil {
		t.Fatalf("ConfigResolve() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal config resolve output: %v", err)
	}
	if stringValueAny(payload["selected_environment"]) != "staging" {
		t.Fatalf("selected_environment = %#v", payload["selected_environment"])
	}
	configPayload, _ := payload["config"].(map[string]any)
	if stringValueAny(configPayload["default_environment"]) != "staging" {
		t.Fatalf("config = %#v", configPayload)
	}
	ingress, _ := configPayload["ingress"].(map[string]any)
	hosts, _ := ingress["hosts"].([]any)
	if len(hosts) != 1 || stringValueAny(hosts[0]) != "staging.example.test" {
		t.Fatalf("ingress hosts = %#v", ingress["hosts"])
	}
}

func TestStatusUsesSavedWorkspaceEnvironment(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{
				{"id": 44, "name": "staging"},
				{"id": 45, "name": "production"},
			}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/45/status":
			return jsonResponse(t, map[string]any{
				"organization":   map[string]any{"id": 7, "name": "default"},
				"project":        map[string]any{"id": 11, "name": "ShopApp"},
				"environment":    map[string]any{"id": 45, "name": "production"},
				"assigned_nodes": 0,
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	if err := app.WorkspaceState.Write(map[string]any{
		"environments": map[string]any{
			root: "production",
		},
	}); err != nil {
		t.Fatalf("write workspace state: %v", err)
	}

	var stdout bytes.Buffer
	app.Printer.Out = &stdout
	app.Printer.JSON = true
	app.Printer.Interactive = false

	if err := app.Status(context.Background(), StatusOptions{}); err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal status output: %v", err)
	}
	environment, _ := payload["environment"].(map[string]any)
	if stringValueAny(environment["name"]) != "production" {
		t.Fatalf("environment = %#v", environment)
	}
}

func TestNodeBootstrapUsesWorkspaceEnvironment(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var captured map[string]any
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "staging"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/organizations/7/node_bootstrap_tokens":
			_ = json.NewDecoder(r.Body).Decode(&captured)
			return jsonResponse(t, map[string]any{"expires_at": "2026-03-15T12:00:00Z", "install_command": "curl ..."}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	if err := app.NodeBootstrap(context.Background(), NodeBootstrapOptions{}); err != nil {
		t.Fatalf("NodeBootstrap() error = %v", err)
	}
	if environmentID := intValueAny(captured["environment_id"]); environmentID != 44 {
		t.Fatalf("environment_id = %v, want 44", captured["environment_id"])
	}
}

func TestNodeBootstrapAutoInitializesWorkspaceWhenConfigMissing(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)

	var captured map[string]any
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 7, "name": "default", "role": "owner"}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 11, "name": filepath.Base(root)}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 44, "name": "production"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/organizations/7/node_bootstrap_tokens":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode bootstrap request: %v", err)
			}
			return jsonResponse(t, map[string]any{"expires_at": "2026-03-15T12:00:00Z", "install_command": "curl ..."}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.NodeBootstrap(context.Background(), NodeBootstrapOptions{}); err != nil {
		t.Fatalf("NodeBootstrap() error = %v", err)
	}

	loaded, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("config was not written during bootstrap auto-init")
	}
	if loaded.Project != filepath.Base(root) {
		t.Fatalf("project = %q, want %q", loaded.Project, filepath.Base(root))
	}
	if environmentID := intValueAny(captured["environment_id"]); environmentID != 44 {
		t.Fatalf("environment_id = %v, want 44", captured["environment_id"])
	}
}

func TestNodeBootstrapUnassignedUsesOrganizationOnly(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var captured map[string]any
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/organizations/7/node_bootstrap_tokens":
			_ = json.NewDecoder(r.Body).Decode(&captured)
			return jsonResponse(t, map[string]any{"expires_at": "2026-03-15T12:00:00Z", "install_command": "curl ...", "assignment_mode": "unassigned"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer = output.New(&stdout, io.Discard, false)

	if err := app.NodeBootstrap(context.Background(), NodeBootstrapOptions{Unassigned: true}); err != nil {
		t.Fatalf("NodeBootstrap() error = %v", err)
	}
	if _, ok := captured["environment_id"]; ok {
		t.Fatalf("environment_id unexpectedly present: %#v", captured)
	}
	if !strings.Contains(stdout.String(), "Unassigned") {
		t.Fatalf("NodeBootstrap() output = %q, want unassigned summary", stdout.String())
	}
}

func TestNodeBootstrapRejectsTrialOrganizationsBeforeAPICall(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("trial-org", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "trial-org", "role": "owner", "plan_tier": "trial"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "staging"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/organizations/7/node_bootstrap_tokens":
			t.Fatal("unexpected bootstrap token creation for trial organization")
			return nil, nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	err := app.NodeBootstrap(context.Background(), NodeBootstrapOptions{})
	if err == nil {
		t.Fatal("NodeBootstrap() error = nil, want trial policy failure")
	}
	if !strings.Contains(err.Error(), "only available in paid organizations") {
		t.Fatalf("NodeBootstrap() error = %v", err)
	}
}

func TestStatusPrintsWarningWhenPresent(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "staging"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/status":
			return jsonResponse(t, map[string]any{
				"organization":   map[string]any{"id": 7, "name": "default"},
				"project":        map[string]any{"id": 11, "name": "ShopApp"},
				"environment":    map[string]any{"id": 44, "name": "staging", "runtime_kind": "customer_nodes"},
				"assigned_nodes": 0,
				"warning":        "No customer-managed nodes are assigned to this environment yet. Run `devopsellence node register`.",
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer = output.New(&stdout, io.Discard, false)

	if err := app.Status(context.Background(), StatusOptions{}); err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Warning:") {
		t.Fatalf("Status() output = %q, want warning prefix", stdout.String())
	}
	if !strings.Contains(stdout.String(), "devopsellence node register") {
		t.Fatalf("Status() output = %q, want register hint", stdout.String())
	}
}

func TestNodeAssignUsesWorkspaceEnvironmentAndNodeID(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var captured map[string]any
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "staging"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/assignments":
			_ = json.NewDecoder(r.Body).Decode(&captured)
			return sseResponse(t, "complete", map[string]any{"node_id": 55, "environment_id": 44, "desired_state_uri": "gs://bucket/nodes/55/desired_state.json"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	if err := app.NodeAssign(context.Background(), NodeAssignOptions{NodeID: 55}); err != nil {
		t.Fatalf("NodeAssign() error = %v", err)
	}
	if nodeID := intValueAny(captured["node_id"]); nodeID != 55 {
		t.Fatalf("node_id = %v, want 55", captured["node_id"])
	}
}

func TestNodeAssignInteractiveSelectsUnassignedNode(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "production")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var captured map[string]any
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations/7/nodes":
			return jsonResponse(t, map[string]any{"nodes": []map[string]any{
				{"id": 10, "name": "node-assigned", "labels": []string{"web"}, "environment": map[string]any{"name": "production"}},
				{"id": 12, "name": "node-revoked", "labels": []string{"worker"}, "revoked_at": "2026-03-29T12:00:00Z"},
				{"id": 55, "name": "node-free", "labels": []string{"worker"}},
			}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/assignments":
			_ = json.NewDecoder(r.Body).Decode(&captured)
			return sseResponse(t, "complete", map[string]any{"node_id": 55, "environment_id": 44, "desired_state_uri": "gs://bucket/nodes/55/desired_state.json"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.In = strings.NewReader("1\n")
	app.Printer.Interactive = true
	if err := app.NodeAssign(context.Background(), NodeAssignOptions{}); err != nil {
		t.Fatalf("NodeAssign() error = %v", err)
	}
	if nodeID := intValueAny(captured["node_id"]); nodeID != 55 {
		t.Fatalf("node_id = %v, want 55", captured["node_id"])
	}
}

func TestNodeUnassignDeletesAssignment(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/cli/nodes/55/assignment":
			return jsonResponse(t, map[string]any{"id": 55, "environment_id": 44}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer.Out = &stdout
	if err := app.NodeUnassign(context.Background(), NodeUnassignOptions{NodeID: 55}); err != nil {
		t.Fatalf("NodeUnassign() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Unassigned node #55 from env #44.") {
		t.Fatalf("NodeUnassign() output = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "devopsellence-agent uninstall --purge-runtime") {
		t.Fatalf("NodeUnassign() output = %q, want uninstall hint", stdout.String())
	}
}

func TestNodeUnassignManagedNodeSignalsDelete(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/cli/nodes/55/assignment":
			return jsonResponse(t, map[string]any{"id": 55, "managed": true}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer.Out = &stdout
	if err := app.NodeUnassign(context.Background(), NodeUnassignOptions{NodeID: 55}); err != nil {
		t.Fatalf("NodeUnassign() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Unassigned managed node #55; server scheduled for delete.") {
		t.Fatalf("NodeUnassign() output = %q", stdout.String())
	}
}

func TestNodeDeleteDeletesUnassignedManagedNode(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/cli/nodes/55":
			return jsonResponse(t, map[string]any{"id": 55, "managed": true, "revoked_at": "2026-04-08T12:00:00Z"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer.Out = &stdout
	if err := app.NodeDelete(context.Background(), NodeDeleteOptions{NodeID: 55}); err != nil {
		t.Fatalf("NodeDelete() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Delete requested for managed node #55; server scheduled for delete.") {
		t.Fatalf("NodeDelete() output = %q", stdout.String())
	}
}

func TestNodeDeleteDeletesUnassignedCustomerManagedNode(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/cli/nodes/55":
			return jsonResponse(t, map[string]any{"id": 55, "managed": false, "revoked_at": "2026-04-08T12:00:00Z"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer.Out = &stdout
	if err := app.NodeDelete(context.Background(), NodeDeleteOptions{NodeID: 55}); err != nil {
		t.Fatalf("NodeDelete() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Removed node #55.") {
		t.Fatalf("NodeDelete() output = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "devopsellence-agent uninstall --purge-runtime") {
		t.Fatalf("NodeDelete() output = %q", stdout.String())
	}
}

func TestSecretSetUsesWorkspaceEnvironment(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var captured map[string]any
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 99, "name": "staging"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/99/secrets":
			_ = json.NewDecoder(r.Body).Decode(&captured)
			return jsonResponse(t, map[string]any{"name": "SECRET_KEY_BASE", "service_name": "web", "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	if err := app.SecretSet(context.Background(), SecretSetOptions{ServiceName: "web", Name: "SECRET_KEY_BASE", Value: "super-secret", ValueProvided: true}); err != nil {
		t.Fatalf("SecretSet() error = %v", err)
	}
	if serviceName := stringValueAny(captured["service_name"]); serviceName != "web" {
		t.Fatalf("service_name = %v, want web", captured["service_name"])
	}
	if value := stringValueAny(captured["value"]); value != "super-secret" {
		t.Fatalf("value = %v, want super-secret", captured["value"])
	}
}

func TestSecretSetReadsValueFromStdin(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var captured map[string]any
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 99, "name": "staging"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/99/secrets":
			_ = json.NewDecoder(r.Body).Decode(&captured)
			return jsonResponse(t, map[string]any{"name": "SECRET_KEY_BASE", "service_name": "web", "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.In = strings.NewReader("super-secret\n")
	if err := app.SecretSet(context.Background(), SecretSetOptions{ServiceName: "web", Name: "SECRET_KEY_BASE", ValueStdin: true}); err != nil {
		t.Fatalf("SecretSet() error = %v", err)
	}
	if value := stringValueAny(captured["value"]); value != "super-secret" {
		t.Fatalf("value = %v, want super-secret", captured["value"])
	}
}

func TestSecretListUsesWorkspaceEnvironment(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 99, "name": "staging"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/99/secrets":
			return jsonResponse(t, map[string]any{"secrets": []map[string]any{
				{"service_name": "web", "name": "SECRET_KEY_BASE", "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"},
				{"service_name": "web", "name": "RAILS_MASTER_KEY", "secret_ref": "gsm://projects/test/secrets/rails/versions/latest"},
			}}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer.Out = &stdout
	if err := app.SecretList(context.Background(), SecretListOptions{}); err != nil {
		t.Fatalf("SecretList() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "SECRET_KEY_BASE") {
		t.Fatalf("SecretList() output = %q, want secret listing", stdout.String())
	}
	if !strings.Contains(stdout.String(), "RAILS_MASTER_KEY -> gsm://projects/test/secrets/rails/versions/latest (auto-managed from config/master.key)") {
		t.Fatalf("SecretList() output = %q, want Rails auto-managed note", stdout.String())
	}
}

func TestSecretDeleteUsesWorkspaceEnvironment(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "staging")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 99, "name": "staging"}}}), nil
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/cli/environments/99/secrets/web/SECRET_KEY_BASE":
			return jsonResponse(t, map[string]any{"name": "SECRET_KEY_BASE", "service_name": "web"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer.Out = &stdout
	if err := app.SecretDelete(context.Background(), SecretDeleteOptions{ServiceName: "web", Name: "SECRET_KEY_BASE"}); err != nil {
		t.Fatalf("SecretDelete() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Deleted secret SECRET_KEY_BASE for web.") {
		t.Fatalf("SecretDelete() output = %q", stdout.String())
	}
}

func TestAliasLFGCreatesSymlinkNextToExecutable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	targetPath := filepath.Join(binDir, "devopsellence")
	if err := os.WriteFile(targetPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))
	app.Printer.Out = &stdout
	app.ExecutablePath = func() (string, error) { return targetPath, nil }
	app.LookPath = func(name string) (string, error) {
		if name != "lfg" {
			t.Fatalf("LookPath() name = %q, want lfg", name)
		}
		return "", exec.ErrNotFound
	}

	if err := app.AliasLFG(context.Background()); err != nil {
		t.Fatalf("AliasLFG() error = %v", err)
	}

	aliasPath := filepath.Join(binDir, "lfg")
	linkTarget, err := os.Readlink(aliasPath)
	if err != nil {
		t.Fatalf("Readlink(%q): %v", aliasPath, err)
	}
	if linkTarget != "devopsellence" {
		t.Fatalf("alias target = %q, want devopsellence", linkTarget)
	}
	if !strings.Contains(stdout.String(), "Created lfg alias at "+aliasPath+".") {
		t.Fatalf("AliasLFG() output = %q", stdout.String())
	}
}

func TestAliasLFGRefusesWhenCommandAlreadyExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))
	app.ExecutablePath = func() (string, error) {
		t.Fatal("ExecutablePath() should not run when lfg already exists")
		return "", nil
	}
	app.LookPath = func(name string) (string, error) {
		if name != "lfg" {
			t.Fatalf("LookPath() name = %q, want lfg", name)
		}
		return "/usr/local/bin/lfg", nil
	}

	err := app.AliasLFG(context.Background())
	if err == nil {
		t.Fatal("AliasLFG() error = nil, want error")
	}

	var exitErr ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("AliasLFG() error = %v, want ExitError", err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("ExitError.Code = %d, want 1", exitErr.Code)
	}
	if !strings.Contains(exitErr.Error(), "lfg already exists at /usr/local/bin/lfg") {
		t.Fatalf("AliasLFG() error = %q", exitErr.Error())
	}
}

func TestNodeListAndLabelSet(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	var labelCaptured map[string]any
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations/7/nodes":
			return jsonResponse(t, map[string]any{"nodes": []map[string]any{
				{"id": 8, "name": "node-a", "labels": []string{"web", "worker"}, "environment": map[string]any{"name": "production", "project_name": "ShopApp"}},
				{"id": 9, "name": "node-b", "labels": []string{"worker"}},
				{"id": 10, "name": "node-c", "labels": []string{"worker"}, "revoked_at": "2026-03-29T12:00:00Z"},
			}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/nodes/8/labels":
			_ = json.NewDecoder(r.Body).Decode(&labelCaptured)
			return jsonResponse(t, map[string]any{"id": 8, "labels": []string{"web", "worker"}}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer.Out = &stdout
	if err := app.NodeList(context.Background(), NodeListOptions{}); err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "node #8") {
		t.Fatalf("NodeList() output = %q, want node listing", stdout.String())
	}
	if !strings.Contains(stdout.String(), "project=ShopApp") {
		t.Fatalf("NodeList() output = %q, want project name", stdout.String())
	}
	if !strings.Contains(stdout.String(), "env=production") {
		t.Fatalf("NodeList() output = %q, want env name", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[unassigned]") {
		t.Fatalf("NodeList() output = %q, want unassigned marker", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[revoked]") {
		t.Fatalf("NodeList() output = %q, want revoked marker", stdout.String())
	}
	if err := app.NodeLabelSet(context.Background(), NodeLabelSetOptions{NodeID: 8, Labels: "web,worker"}); err != nil {
		t.Fatalf("NodeLabelSet() error = %v", err)
	}
	if labels := stringValueAny(labelCaptured["labels"]); labels != "web,worker" {
		t.Fatalf("labels = %v, want web,worker", labelCaptured["labels"])
	}
}

func TestNodeDiagnose(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	var stdout bytes.Buffer
	polls := 0
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/nodes/8/diagnose_requests":
			return jsonResponseWithStatus(t, http.StatusAccepted, map[string]any{
				"id":           41,
				"status":       "pending",
				"requested_at": "2026-03-29T20:00:00Z",
				"node":         map[string]any{"id": 8, "name": "node-a", "organization_id": 7},
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/node_diagnose_requests/41":
			polls++
			if polls == 1 {
				return jsonResponse(t, map[string]any{
					"id":           41,
					"status":       "claimed",
					"requested_at": "2026-03-29T20:00:00Z",
					"claimed_at":   "2026-03-29T20:00:01Z",
					"node":         map[string]any{"id": 8, "name": "node-a", "organization_id": 7},
				}), nil
			}
			return jsonResponse(t, map[string]any{
				"id":           41,
				"status":       "completed",
				"requested_at": "2026-03-29T20:00:00Z",
				"claimed_at":   "2026-03-29T20:00:01Z",
				"completed_at": "2026-03-29T20:00:02Z",
				"node":         map[string]any{"id": 8, "name": "node-a", "organization_id": 7},
				"result": map[string]any{
					"collected_at":  "2026-03-29T20:00:02Z",
					"agent_version": "devopsellence-agent/dev",
					"summary": map[string]any{
						"status":        "degraded",
						"total":         1,
						"running":       0,
						"stopped":       1,
						"unhealthy":     0,
						"logs_included": 1,
					},
					"containers": []map[string]any{
						{
							"name":            "devopsellence-web",
							"service":         "web",
							"image":           "shop-app@sha256:abc",
							"running":         false,
							"has_healthcheck": true,
							"log_tail":        "boot failed",
						},
					},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer.Out = &stdout

	if err := app.NodeDiagnose(context.Background(), NodeDiagnoseOptions{NodeID: 8, Wait: 2 * time.Second}); err != nil {
		t.Fatalf("NodeDiagnose() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Node diagnose #41") {
		t.Fatalf("NodeDiagnose() output = %q, want request header", stdout.String())
	}
	if !strings.Contains(stdout.String(), "status=degraded") {
		t.Fatalf("NodeDiagnose() output = %q, want summary", stdout.String())
	}
	if !strings.Contains(stdout.String(), "boot failed") {
		t.Fatalf("NodeDiagnose() output = %q, want log tail", stdout.String())
	}
}

func TestDeployWaitsForRolloutProgress(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "production")); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	progressCalls := 0
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"secrets": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"name": "RAILS_MASTER_KEY", "service_name": "web", "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 2,
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			progressCalls++
			if progressCalls == 1 {
				return jsonResponse(t, map[string]any{
					"id":       77,
					"sequence": 3,
					"status":   "published",
					"environment": map[string]any{
						"id":   44,
						"name": "production",
					},
					"release": map[string]any{
						"id":       22,
						"revision": "rel-1",
					},
					"summary": map[string]any{
						"assigned_nodes": 2,
						"pending":        1,
						"reconciling":    1,
						"settled":        0,
						"error":          0,
						"active":         true,
						"complete":       false,
						"failed":         false,
					},
					"nodes": []map[string]any{
						{"id": 1, "name": "node-a", "phase": "reconciling", "message": "pulling image"},
						{"id": 2, "name": "node-b", "phase": "pending", "message": "waiting for node to reconcile"},
					},
				}), nil
			}
			return jsonResponse(t, map[string]any{
				"id":       77,
				"sequence": 3,
				"status":   "published",
				"environment": map[string]any{
					"id":   44,
					"name": "production",
				},
				"release": map[string]any{
					"id":       22,
					"revision": "rel-1",
				},
				"summary": map[string]any{
					"assigned_nodes": 2,
					"pending":        0,
					"reconciling":    0,
					"settled":        2,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
					{"id": 2, "name": "node-b", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer = output.New(&stdout, io.Discard, false)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if progressCalls < 2 {
		t.Fatalf("progressCalls = %d, want at least 2", progressCalls)
	}
	if !strings.Contains(stdout.String(), "rollout pending=1 reconciling=1 settled=0 error=0") {
		t.Fatalf("Deploy() output = %q, want rollout progress", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Deploy complete.") {
		t.Fatalf("Deploy() output = %q, want completion", stdout.String())
	}
	if strings.Contains(stdout.String(), "Release ID") || strings.Contains(stdout.String(), "Deploy ID") {
		t.Fatalf("Deploy() output = %q, want ids omitted", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Total") {
		t.Fatalf("Deploy() output = %q, want timing rows", stdout.String())
	}
}

func TestDeployUsesResolveDeployTargetWhenAvailable(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	project.Ingress = &config.IngressConfig{
		Hosts: []string{"app.example.com"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "app.example.com", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
		}},
	}
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	var releaseCaptured api.ReleaseCreateRequest
	app := newTestAppWithDeployTarget(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/deploy_target":
			return jsonResponse(t, map[string]any{
				"organization":         map[string]any{"id": 7, "name": "default"},
				"organization_created": false,
				"project":              map[string]any{"id": 11, "name": filepath.Base(root), "organization_id": 7},
				"project_created":      false,
				"environment":          map[string]any{"id": 44, "name": "production", "project_id": 11},
				"environment_created":  false,
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			t.Fatalf("unexpected legacy organizations lookup: %s %s", r.Method, r.URL.Path)
			return nil, nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			t.Fatalf("unexpected legacy projects lookup: %s %s", r.Method, r.URL.Path)
			return nil, nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			t.Fatalf("unexpected legacy environments lookup: %s %s", r.Method, r.URL.Path)
			return nil, nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			if err := json.NewDecoder(r.Body).Decode(&releaseCaptured); err != nil {
				t.Fatalf("decode release request: %v", err)
			}
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://generic.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{
		Image:          "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NonInteractive: true,
	}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if releaseCaptured.ImageRepository != "docker.io/mccutchen/go-httpbin" {
		t.Fatalf("image repository = %q, want docker.io/mccutchen/go-httpbin", releaseCaptured.ImageRepository)
	}
	if got := stringValueAny(releaseCaptured.Ingress["hosts"].([]any)[0]); got != "app.example.com" {
		t.Fatalf("ingress host = %q, want app.example.com", got)
	}
	if got := stringValueAny(releaseCaptured.Ingress["rules"].([]any)[0].(map[string]any)["target"].(map[string]any)["service"]); got != config.DefaultWebServiceName {
		t.Fatalf("ingress target service = %q, want %q", got, config.DefaultWebServiceName)
	}
}

func TestDeployAppliesGitHubActionRuntimeOverrides(t *testing.T) {
	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	web := project.Services[config.DefaultWebServiceName]
	web.Env = map[string]string{"FROM_CONFIG": "1"}
	project.Services[config.DefaultWebServiceName] = web
	project.Services["worker"] = config.Service{
		Kind:    config.ServiceKindWorker,
		Command: []string{"./bin/jobs"},
		Env:     map[string]string{"WORKER_FROM_CONFIG": "1"},
	}
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	t.Setenv(deployEnvVarsOverrideEnv, `{"all":{"RAILS_ENV":"production"},"web":{"WEB_ONLY":"true"},"worker":{"QUEUE":"critical"}}`)
	t.Setenv(deploySecretsOverrideEnv, `{"all":{"DATABASE_URL":"postgres://db"},"worker":{"REDIS_URL":"redis://cache"}}`)

	var releaseCaptured api.ReleaseCreateRequest
	var secretPayloads []map[string]any
	app := newTestAppWithDeployTarget(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/deploy_target":
			return jsonResponse(t, map[string]any{
				"organization":         map[string]any{"id": 7, "name": "default"},
				"organization_created": false,
				"project":              map[string]any{"id": 11, "name": filepath.Base(root), "organization_id": 7},
				"project_created":      false,
				"environment":          map[string]any{"id": 44, "name": "production", "project_id": 11},
				"environment_created":  false,
			}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode secret payload: %v", err)
			}
			secretPayloads = append(secretPayloads, payload)
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"name":         payload["name"],
				"service_name": payload["service_name"],
				"secret_ref":   "gsm://projects/test/secrets/" + payload["name"].(string) + "/versions/latest",
			}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			if err := json.NewDecoder(r.Body).Decode(&releaseCaptured); err != nil {
				t.Fatalf("decode release request: %v", err)
			}
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://generic.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{
		Image:          "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NonInteractive: true,
	}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}

	webPayload := releaseServicePayload(t, releaseCaptured, config.DefaultWebServiceName)
	workerPayload := releaseServicePayload(t, releaseCaptured, "worker")
	if got, want := webPayload["env"], map[string]any{"FROM_CONFIG": "1", "RAILS_ENV": "production", "WEB_ONLY": "true"}; !equalJSONMap(got, want) {
		t.Fatalf("web env = %#v, want %#v", got, want)
	}
	if got, want := workerPayload["env"], map[string]any{"WORKER_FROM_CONFIG": "1", "RAILS_ENV": "production", "QUEUE": "critical"}; !equalJSONMap(got, want) {
		t.Fatalf("worker env = %#v, want %#v", got, want)
	}

	wantSecrets := []map[string]any{
		{"service_name": "web", "name": "DATABASE_URL", "value": "postgres://db"},
		{"service_name": "worker", "name": "DATABASE_URL", "value": "postgres://db"},
		{"service_name": "worker", "name": "REDIS_URL", "value": "redis://cache"},
	}
	if !equalSecretPayloads(secretPayloads, wantSecrets) {
		t.Fatalf("secret payloads = %#v, want %#v", secretPayloads, wantSecrets)
	}
}

func TestDeployShowsSchedulingStatusWhileManagedNodeBoots(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "production")); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	progressCalls := 0
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"secrets": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"name": "RAILS_MASTER_KEY", "service_name": "web", "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 0,
				"status":         "scheduling",
				"status_message": "booting managed node",
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			progressCalls++
			if progressCalls == 1 {
				return jsonResponse(t, map[string]any{
					"id":             77,
					"sequence":       3,
					"status":         "scheduling",
					"status_message": "booting managed node",
					"environment":    map[string]any{"id": 44, "name": "production"},
					"release":        map[string]any{"id": 22, "revision": "rel-1"},
					"summary": map[string]any{
						"assigned_nodes": 0,
						"pending":        0,
						"reconciling":    0,
						"settled":        0,
						"error":          0,
						"active":         true,
						"complete":       false,
						"failed":         false,
					},
					"nodes": []map[string]any{},
				}), nil
			}
			return jsonResponse(t, map[string]any{
				"id":             77,
				"sequence":       3,
				"status":         "published",
				"status_message": "rollout settled",
				"environment":    map[string]any{"id": 44, "name": "production"},
				"release":        map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer = output.New(&stdout, io.Discard, false)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "rollout pending=0 reconciling=0 settled=0 error=0 - booting managed node") {
		t.Fatalf("Deploy() output = %q, want scheduling detail", stdout.String())
	}
	if !strings.Contains(stdout.String(), "milestone: managed capacity requested; waiting for the node to boot") {
		t.Fatalf("Deploy() output = %q, want scheduling milestone", stdout.String())
	}
}

func TestDeployShowsWarmCapacityMilestones(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "production")); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	progressCalls := 0
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"secrets": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"name": "RAILS_MASTER_KEY", "service_name": "web", "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 0,
				"status":         "scheduling",
				"status_message": "waiting for managed capacity",
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			progressCalls++
			switch progressCalls {
			case 1:
				return jsonResponse(t, map[string]any{
					"id":             77,
					"sequence":       3,
					"status":         "scheduling",
					"status_message": "claiming node bundle",
					"environment":    map[string]any{"id": 44, "name": "production"},
					"release":        map[string]any{"id": 22, "revision": "rel-1"},
					"summary":        map[string]any{"assigned_nodes": 0, "pending": 0, "reconciling": 0, "settled": 0, "error": 0, "active": true, "complete": false, "failed": false},
					"nodes":          []map[string]any{},
				}), nil
			case 2:
				return jsonResponse(t, map[string]any{
					"id":             77,
					"sequence":       3,
					"status":         "rolling_out",
					"status_message": "publishing desired state",
					"environment":    map[string]any{"id": 44, "name": "production"},
					"release":        map[string]any{"id": 22, "revision": "rel-1"},
					"summary":        map[string]any{"assigned_nodes": 1, "pending": 0, "reconciling": 0, "settled": 0, "error": 0, "active": true, "complete": false, "failed": false},
					"nodes":          []map[string]any{},
				}), nil
			case 3:
				return jsonResponse(t, map[string]any{
					"id":             77,
					"sequence":       3,
					"status":         "rolling_out",
					"status_message": "waiting for node reconcile",
					"environment":    map[string]any{"id": 44, "name": "production"},
					"release":        map[string]any{"id": 22, "revision": "rel-1"},
					"summary":        map[string]any{"assigned_nodes": 1, "pending": 1, "reconciling": 0, "settled": 0, "error": 0, "active": true, "complete": false, "failed": false},
					"nodes": []map[string]any{
						{"id": 1, "name": "node-a", "phase": "pending", "message": "waiting for node to reconcile"},
					},
				}), nil
			default:
				return jsonResponse(t, map[string]any{
					"id":             77,
					"sequence":       3,
					"status":         "published",
					"status_message": "rollout settled",
					"environment":    map[string]any{"id": 44, "name": "production"},
					"release":        map[string]any{"id": 22, "revision": "rel-1"},
					"summary":        map[string]any{"assigned_nodes": 1, "pending": 0, "reconciling": 0, "settled": 1, "error": 0, "active": false, "complete": true, "failed": false},
					"nodes": []map[string]any{
						{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
					},
				}), nil
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer = output.New(&stdout, io.Discard, false)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	for _, fragment := range []string{
		"milestone: warm capacity available; claiming a node bundle",
		"milestone: capacity claimed; publishing desired state to the node",
		"milestone: node claimed; waiting for the agent to apply the new revision",
		"milestone: new revision is healthy",
	} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("Deploy() output = %q, want %q", stdout.String(), fragment)
		}
	}
}

func TestDeployReportsManagedCapacityFallback(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "production")); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	const capacityError = "No managed server capacity is available in ash/cpx11 right now. Retry in a few minutes, or use your own VM/server with `devopsellence mode use solo` and `devopsellence setup`."

	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"secrets": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"name": "RAILS_MASTER_KEY", "service_name": "web", "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 0,
				"status":         "scheduling",
				"status_message": "waiting for managed capacity",
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":             77,
				"sequence":       3,
				"status":         "failed",
				"status_message": "publish failed",
				"error_message":  capacityError,
				"environment":    map[string]any{"id": 44, "name": "production"},
				"release":        map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 0,
					"pending":        0,
					"reconciling":    0,
					"settled":        0,
					"error":          0,
					"active":         false,
					"complete":       false,
					"failed":         true,
				},
				"nodes": []map[string]any{},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer = output.New(&stdout, io.Discard, false)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err == nil {
		t.Fatal("Deploy() error = nil, want managed capacity failure")
	}
	if !strings.Contains(err.Error(), capacityError) {
		t.Fatalf("Deploy() error = %q, want %q", err.Error(), capacityError)
	}
	if !strings.Contains(stdout.String(), capacityError) {
		t.Fatalf("Deploy() output = %q, want %q", stdout.String(), capacityError)
	}
}

func TestDeployRetriesPublishAfter524WithSameRequestToken(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "production")); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	publishCalls := 0
	requestTokens := []string{}
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"secrets": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"name": "RAILS_MASTER_KEY", "service_name": "web", "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			publishCalls++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode publish request: %v", err)
			}
			requestTokens = append(requestTokens, stringValueAny(payload["request_token"]))
			if publishCalls == 1 {
				return jsonResponseWithStatus(t, 524, map[string]any{"error": "timeout"}), nil
			}
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"status":         "published",
				"status_message": "waiting for node reconcile",
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":             77,
				"sequence":       1,
				"status":         "published",
				"status_message": "rollout settled",
				"environment":    map[string]any{"id": 44, "name": "production"},
				"release":        map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if publishCalls != 2 {
		t.Fatalf("publishCalls = %d, want 2", publishCalls)
	}
	if len(requestTokens) != 2 || requestTokens[0] == "" || requestTokens[0] != requestTokens[1] {
		t.Fatalf("request tokens = %#v, want same non-empty token", requestTokens)
	}
}

func TestDeployBuildsAndPushesMultiArchImage(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	project := config.DefaultProjectConfig("default", "ShopApp", "production")
	project.Build.Platforms = []string{"linux/amd64", "linux/arm64"}
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	var releaseCaptured api.ReleaseCreateRequest
	dockerStub := &fakeDocker{
		digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		delay:  25 * time.Millisecond,
	}
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"secrets": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"name": "RAILS_MASTER_KEY", "service_name": "web", "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/registry/push_auth":
			return jsonResponse(t, map[string]any{
				"registry_host":    "northamerica-northeast1-docker.pkg.dev",
				"repository_path":  "northamerica-northeast1-docker.pkg.dev/devopsellence-dev/org-1-apps",
				"image_repository": "shopapp",
				"docker_username":  "oauth2accesstoken",
				"docker_password":  "gar-token",
			}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			if err := json.NewDecoder(r.Body).Decode(&releaseCaptured); err != nil {
				t.Fatalf("decode release request: %v", err)
			}
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":       77,
				"sequence": 1,
				"status":   "published",
				"environment": map[string]any{
					"id":   44,
					"name": "production",
				},
				"release": map[string]any{
					"id":       22,
					"revision": "rel-1",
				},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Docker = dockerStub
	app.Printer = output.New(&stdout, io.Discard, false)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if strings.Join(dockerStub.buildPlatforms, ",") != "linux/amd64,linux/arm64" {
		t.Fatalf("build platforms = %#v", dockerStub.buildPlatforms)
	}
	if !strings.Contains(dockerStub.buildTarget, "northamerica-northeast1-docker.pkg.dev/devopsellence-dev/org-1-apps/shopapp:") {
		t.Fatalf("build target = %q", dockerStub.buildTarget)
	}
	if releaseCaptured.ImageDigest != dockerStub.digest {
		t.Fatalf("image digest = %q, want %q", releaseCaptured.ImageDigest, dockerStub.digest)
	}
	if releaseCaptured.ImageRepository != "shopapp" {
		t.Fatalf("image repository = %q, want shopapp", releaseCaptured.ImageRepository)
	}
	if !strings.Contains(stdout.String(), "Image Build/Push") || !strings.Contains(stdout.String(), "Control Plane") || !strings.Contains(stdout.String(), "Total") {
		t.Fatalf("Deploy() output = %q, want timing summary", stdout.String())
	}
}

func TestDeleteUsesWorkspaceEnvironment(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "production")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}, {"id": 55, "name": "staging"}}}), nil
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/cli/environments/55":
			return jsonResponse(t, map[string]any{
				"id":                55,
				"name":              "staging",
				"customer_node_ids": []int{8, 9},
				"managed_node_ids":  []int{12},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer = output.New(&stdout, io.Discard, false)

	if err := app.Delete(context.Background(), DeleteOptions{Environment: "staging"}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Deleted environment staging.") {
		t.Fatalf("Delete() output = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Customer nodes unassigned: 2 Managed servers scheduled for delete: 1") {
		t.Fatalf("Delete() output = %q", stdout.String())
	}
}

func TestDeployRailsSyncsMasterKeySecret(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	project := config.DefaultProjectConfig("default", "ShopApp", "production")
	project.Services["worker"] = config.ServiceConfig{Kind: config.ServiceKindWorker, Command: []string{"bin/jobs"}}
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "master.key"), []byte("master-key-value\n"), 0o600); err != nil {
		t.Fatalf("write master.key: %v", err)
	}
	commitAll(t, root, "configure deploy state")

	secretValues := map[string]string{}
	var secretValuesMu sync.Mutex
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"secrets": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode secret payload: %v", err)
			}
			secretValuesMu.Lock()
			secretValues[stringValueAny(payload["service_name"])] = stringValueAny(payload["value"])
			secretValuesMu.Unlock()
			return jsonResponse(t, map[string]any{"name": stringValueAny(payload["name"]), "service_name": stringValueAny(payload["service_name"]), "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer = output.New(&stdout, io.Discard, false)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	secretValuesMu.Lock()
	if got := secretValues["web"]; got != "master-key-value" {
		secretValuesMu.Unlock()
		t.Fatalf("web secret value = %q, want master-key-value", got)
	}
	if got := secretValues["worker"]; got != "master-key-value" {
		secretValuesMu.Unlock()
		t.Fatalf("worker secret value = %q, want master-key-value", got)
	}
	secretValuesMu.Unlock()
	if !strings.Contains(stdout.String(), "Rails: syncing RAILS_MASTER_KEY from config/master.key for web, worker.") {
		t.Fatalf("Deploy() output = %q, want explicit Rails sync notice", stdout.String())
	}
	if !strings.Contains(stdout.String(), "synced RAILS_MASTER_KEY for web, worker.") {
		t.Fatalf("Deploy() output = %q, want Rails secret sync summary", stdout.String())
	}
}

func TestDeployRailsCanSkipMasterKeySync(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	project := config.DefaultProjectConfig("default", "ShopApp", "production")
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "master.key"), []byte("master-key-value\n"), 0o600); err != nil {
		t.Fatalf("write master.key: %v", err)
	}
	commitAll(t, root, "configure deploy state")

	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"secrets": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			t.Fatalf("unexpected Rails secret sync request")
			return nil, nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer = output.New(&stdout, io.Discard, false)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{
		Image:                  "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SkipRailsMasterKeySync: true,
	}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Rails: skipping RAILS_MASTER_KEY sync (--no-rails-master-key-sync).") {
		t.Fatalf("Deploy() output = %q, want skip notice", stdout.String())
	}
	if strings.Contains(stdout.String(), "synced RAILS_MASTER_KEY") {
		t.Fatalf("Deploy() output = %q, unexpected sync summary", stdout.String())
	}
}

func TestDeployRailsSkipsNoOpMasterKeyUpsertWhenDigestMatches(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	project := config.DefaultProjectConfig("default", "ShopApp", "production")
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "master.key"), []byte("master-key-value\n"), 0o600); err != nil {
		t.Fatalf("write master.key: %v", err)
	}
	commitAll(t, root, "configure deploy state")

	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{
				"secrets": []map[string]any{{
					"service_name": "web",
					"name":         "RAILS_MASTER_KEY",
					"secret_ref":   "gsm://projects/test/secrets/abc/versions/latest",
					"value_sha256": "b244ce38fa8ece00bd70b7528e38e96c958c7afec2d06d30c9a532c307a3221c",
				}},
			}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			t.Fatalf("unexpected Rails secret sync request")
			return nil, nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer = output.New(&stdout, io.Discard, false)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Rails: RAILS_MASTER_KEY already current for web.") {
		t.Fatalf("Deploy() output = %q, want current-secret notice", stdout.String())
	}
	if strings.Contains(stdout.String(), "synced RAILS_MASTER_KEY") {
		t.Fatalf("Deploy() output = %q, unexpected sync summary", stdout.String())
	}
}

func TestDeployRailsAllowsMissingMasterKey(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	if err := os.Remove(filepath.Join(root, "config", "master.key")); err != nil {
		t.Fatalf("remove master.key: %v", err)
	}
	project := config.DefaultProjectConfig("default", "ShopApp", "production")
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "configure deploy state")

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			t.Fatalf("unexpected secret lookup for Rails app without master.key")
			return nil, nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			t.Fatalf("unexpected secret sync for Rails app without master.key")
			return nil, nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{
		Image:          "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NonInteractive: true,
	}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func TestDeployBuildsGenericWorkspaceImage(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	web := project.Services[config.DefaultWebServiceName]
	for i := range web.Ports {
		if web.Ports[i].Name == "http" {
			web.Ports[i].Port = 8080
		}
	}
	web.Healthcheck.Path = "/"
	web.Healthcheck.Port = 8080
	project.Services[config.DefaultWebServiceName] = web
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	var releaseCaptured api.ReleaseCreateRequest
	dockerStub := &fakeDocker{
		digest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": filepath.Base(root)}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/registry/push_auth":
			return jsonResponse(t, map[string]any{
				"registry_host":    "northamerica-northeast1-docker.pkg.dev",
				"repository_path":  "northamerica-northeast1-docker.pkg.dev/devopsellence-dev/org-1-apps",
				"image_repository": "generic-app",
				"docker_username":  "oauth2accesstoken",
				"docker_password":  "gar-token",
			}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			if err := json.NewDecoder(r.Body).Decode(&releaseCaptured); err != nil {
				t.Fatalf("decode release request: %v", err)
			}
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://generic.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Docker = dockerStub
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	webPayload := releaseServicePayload(t, releaseCaptured, config.DefaultWebServiceName)
	if webPayload["env"] == nil {
		t.Fatalf("web env payload missing")
	}
	envMap, ok := webPayload["env"].(map[string]any)
	if !ok {
		t.Fatalf("web env payload type = %T", webPayload["env"])
	}
	if _, exists := envMap["RAILS_MASTER_KEY"]; exists {
		t.Fatalf("generic deploy should not inject RAILS_MASTER_KEY: %#v", envMap)
	}
}

func TestDeployAutoInitializesWorkspaceWhenConfigMissing(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	var releaseCaptured api.ReleaseCreateRequest

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 7, "name": "default", "role": "owner"}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 11, "name": filepath.Base(root)}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 44, "name": "production"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			if err := json.NewDecoder(r.Body).Decode(&releaseCaptured); err != nil {
				t.Fatalf("decode release request: %v", err)
			}
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://generic.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{Image: "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}

	loaded, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("config was not written during auto-init")
	}
	if loaded.Project != filepath.Base(root) {
		t.Fatalf("project = %q, want %q", loaded.Project, filepath.Base(root))
	}
	if releaseCaptured.ImageRepository != "docker.io/mccutchen/go-httpbin" {
		t.Fatalf("image repository = %q, want docker.io/mccutchen/go-httpbin", releaseCaptured.ImageRepository)
	}
	if releaseCaptured.ImageDigest != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("image digest = %q", releaseCaptured.ImageDigest)
	}
}

func TestDeployAutoInitInfersPortFromBuiltImageMetadata(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	var releaseCaptured api.ReleaseCreateRequest
	dockerStub := &fakeDocker{
		digest:        "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		imageMetadata: docker.ImageMetadata{ExposedPorts: []int{80}},
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 7, "name": "default", "role": "owner"}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 11, "name": filepath.Base(root)}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 44, "name": "production"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/registry/push_auth":
			return jsonResponse(t, map[string]any{
				"registry_host":    "northamerica-northeast1-docker.pkg.dev",
				"repository_path":  "northamerica-northeast1-docker.pkg.dev/devopsellence-dev/org-1-apps",
				"image_repository": "generic-app",
				"docker_username":  "oauth2accesstoken",
				"docker_password":  "gar-token",
			}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			if err := json.NewDecoder(r.Body).Decode(&releaseCaptured); err != nil {
				t.Fatalf("decode release request: %v", err)
			}
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://generic.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Docker = dockerStub
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}

	loaded, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("config was not written during auto-init")
	}
	web := webService(t, loaded)
	if web.HTTPPort(0) != 80 {
		t.Fatalf("web http port = %d, want 80", web.HTTPPort(0))
	}
	if web.Healthcheck == nil || web.Healthcheck.Port != 80 {
		t.Fatalf("healthcheck = %#v, want port 80", web.Healthcheck)
	}
	webPayload := releaseServicePayload(t, releaseCaptured, config.DefaultWebServiceName)
	if port := servicePayloadHTTPPort(webPayload); port != 80 {
		t.Fatalf("release web http port = %v, want 80", webPayload["ports"])
	}
	healthcheck, ok := webPayload["healthcheck"].(map[string]any)
	if !ok {
		t.Fatalf("release healthcheck payload missing: %#v", webPayload["healthcheck"])
	}
	if port := intValueAny(healthcheck["port"]); port != 80 {
		t.Fatalf("release healthcheck.port = %v, want 80", healthcheck["port"])
	}
}

func TestDeployRecoversFromUnauthorizedAPIResponse(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	var (
		orgCalls     int
		refreshCalls int
	)
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			orgCalls++
			if r.Header.Get("Authorization") == "Bearer token" {
				return jsonResponseWithStatus(t, http.StatusUnauthorized, map[string]any{"error": "invalid access token"}), nil
			}
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/auth/refresh":
			refreshCalls++
			return jsonResponse(t, map[string]any{
				"access_token":  "fresh-token",
				"refresh_token": "fresh-refresh",
				"token_type":    "Bearer",
				"expires_in":    3600,
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			if r.Header.Get("Authorization") != "Bearer fresh-token" {
				t.Fatalf("projects auth = %q, want refreshed token", r.Header.Get("Authorization"))
			}
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": filepath.Base(root)}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://generic.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{
		Image:          "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NonInteractive: true,
	}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if orgCalls < 2 {
		t.Fatalf("organization calls = %d, want retry after unauthorized", orgCalls)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
}

func TestInitInfersThrustPortForFirstGeneratedConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Gemfile"), []byte("source 'https://rubygems.org'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "application.rb"), []byte("module SmokePort\n  class Application < Rails::Application\n  end\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM ruby:4.0.0-slim\nEXPOSE 80\nCMD [\"./bin/thrust\", \"./bin/rails\", \"server\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"id": 11, "organization_id": 7, "name": "SmokePort"}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"id": 13, "project_id": 11, "name": "production"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.Init(context.Background(), InitOptions{Organization: "default", ProjectName: "SmokePort", NonInteractive: true}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	loaded, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("config was not written")
	}
	web := webService(t, loaded)
	if web.HTTPPort(0) != 80 {
		t.Fatalf("web http port = %d, want 80", web.HTTPPort(0))
	}
	if web.Healthcheck == nil || web.Healthcheck.Port != 80 {
		t.Fatalf("healthcheck.port = %#v, want 80", web.Healthcheck)
	}
}

func TestDeployBootstrapsAnonymousTrialWhenStateMissing(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	var bootstrapCalls int
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/public/cli/bootstrap":
			bootstrapCalls++
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"access_token":  "anon-token",
				"refresh_token": "anon-refresh",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"account_kind":  "anonymous",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			if r.Header.Get("Authorization") != "Bearer anon-token" {
				t.Fatalf("organizations auth = %q, want anonymous token", r.Header.Get("Authorization"))
			}
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": filepath.Base(root)}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":    77,
				"assigned_nodes":   1,
				"public_url":       "https://generic.example.test",
				"trial_expires_at": "2026-03-22T20:00:00Z",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	if deleted, err := app.State.Delete(); err != nil || !deleted {
		t.Fatalf("Delete() = (%v, %v), want (true, nil)", deleted, err)
	}
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{
		Image:          "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NonInteractive: true,
	}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if bootstrapCalls != 1 {
		t.Fatalf("bootstrap calls = %d, want 1", bootstrapCalls)
	}
}

func TestClaimStartsAnonymousAccountClaim(t *testing.T) {
	t.Parallel()

	root := makeGenericRoot(t)
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/account/claim/start":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode claim payload: %v", err)
			}
			if payload["email"] != "claim@example.com" {
				t.Fatalf("email = %#v, want claim@example.com", payload["email"])
			}
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"status":  "ok",
				"email":   "claim@example.com",
				"message": "Check your email to claim your account.",
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	if err := app.State.Write(map[string]any{
		"access_token":     "token",
		"refresh_token":    "refresh-token",
		"api_base":         "https://dev.devopsellence.test",
		"expires_at":       time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
		"account_kind":     "anonymous",
		"anonymous_id":     "anon-123",
		"anonymous_secret": "secret-123",
	}); err != nil {
		t.Fatalf("write state: %v", err)
	}

	if err := app.Claim(context.Background(), ClaimOptions{Email: "claim@example.com"}); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
}

func TestTokenListPrintsCurrentAndRevokedTokens(t *testing.T) {
	t.Parallel()

	root := makeGenericRoot(t)
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/tokens":
			return jsonResponse(t, map[string]any{"tokens": []map[string]any{
				{"id": 10, "name": "deploy", "created_at": "2026-03-28T19:00:00Z", "current": true, "last_used_at": "2026-03-28T19:05:00Z"},
				{"id": 11, "name": "old", "created_at": "2026-03-27T19:00:00Z", "revoked_at": "2026-03-28T18:00:00Z"},
			}}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	var stdout bytes.Buffer
	app.Printer = output.New(&stdout, &stdout, false)

	if err := app.TokenList(context.Background(), TokenListOptions{}); err != nil {
		t.Fatalf("TokenList() error = %v", err)
	}
	text := stdout.String()
	for _, snippet := range []string{"#10", "deploy", "current", "#11", "revoked"} {
		if !strings.Contains(text, snippet) {
			t.Fatalf("output missing %q: %q", snippet, text)
		}
	}
}

func TestTokenRevokeRevokesByID(t *testing.T) {
	t.Parallel()

	root := makeGenericRoot(t)
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/cli/tokens/12":
			return jsonResponse(t, map[string]any{"id": 12, "name": "deploy", "revoked_at": "2026-03-28T19:00:00Z"}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.TokenRevoke(context.Background(), TokenRevokeOptions{ID: 12}); err != nil {
		t.Fatalf("TokenRevoke() error = %v", err)
	}
}

func TestProjectDeleteDeletesByName(t *testing.T) {
	t.Parallel()

	root := makeGenericRoot(t)
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "production")); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner", "plan_tier": "paid"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/cli/projects/11":
			return jsonResponse(t, map[string]any{"id": 11, "name": "ShopApp", "organization_id": 7, "deleted": true}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	if err := app.ProjectDelete(context.Background(), ProjectDeleteOptions{Name: "ShopApp"}); err != nil {
		t.Fatalf("ProjectDelete() error = %v", err)
	}
}

func TestDeployClaimReminderPrintsOncePerAnonymousAccount(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "configure deploy state")

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner", "plan_tier": "trial"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": filepath.Base(root)}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":    33,
				"assigned_nodes":   1,
				"status":           "queued",
				"status_message":   "Queued",
				"public_url":       "https://shop.example.test",
				"trial_expires_at": "2026-03-29T19:00:00Z",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/33":
			return jsonResponse(t, map[string]any{
				"id":       33,
				"sequence": 1,
				"status":   "settled",
				"summary": map[string]any{
					"assigned_nodes": 1,
					"settled":        1,
					"complete":       true,
				},
				"nodes": []map[string]any{},
				"ingress": map[string]any{
					"public_url": "https://shop.example.test",
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	if err := app.State.Write(map[string]any{
		"access_token":                     "token",
		"refresh_token":                    "refresh-token",
		"api_base":                         "https://dev.devopsellence.test",
		"expires_at":                       time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
		"account_kind":                     "anonymous",
		"anonymous_id":                     "anon-123",
		"anonymous_secret":                 "secret-123",
		"last_claim_reminder_anonymous_id": "",
	}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.Printer = output.New(&stdout, &stderr, false)
	app.Docker = &fakeDocker{imageMetadata: docker.ImageMetadata{ExposedPorts: []int{3000}}}

	opts := DeployOptions{
		Image:          "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NonInteractive: true,
	}
	if err := app.Deploy(context.Background(), opts); err != nil {
		t.Fatalf("first Deploy() error = %v", err)
	}
	firstOutput := stdout.String()
	if !strings.Contains(firstOutput, "Claim this account before local state is lost") {
		t.Fatalf("first deploy output missing claim reminder: %q", firstOutput)
	}

	stdout.Reset()
	stderr.Reset()
	if err := app.Deploy(context.Background(), opts); err != nil {
		t.Fatalf("second Deploy() error = %v", err)
	}
	if strings.Contains(stdout.String(), "Claim this account before local state is lost") {
		t.Fatalf("second deploy output unexpectedly repeated claim reminder: %q", stdout.String())
	}
}

func TestDeployFailsFastWhenDockerDaemonUnavailable(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected API request before docker preflight: %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))
	app.Docker = &dockerUnavailableStub{}

	err := app.Deploy(context.Background(), DeployOptions{NonInteractive: true})
	if err == nil {
		t.Fatal("Deploy() error = nil, want docker daemon failure")
	}
	if !strings.Contains(err.Error(), "Docker Engine is not running or not reachable") {
		t.Fatalf("Deploy() error = %v", err)
	}
	if !strings.Contains(err.Error(), "Docker-compatible local engine") {
		t.Fatalf("Deploy() error = %v", err)
	}
	if !strings.Contains(err.Error(), "devopsellence deploy --image docker.io/mccutchen/go-httpbin@sha256:809250d14e94397f4729f617931068a9ea048231fc1a11c9e3c7cb8c28bbab8d") {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func TestDeployFailsBeforeRemoteWritesWhenBuildInputsInvalid(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	project := config.DefaultProjectConfig("default", "ShopApp", "production")
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")
	if err := os.Remove(filepath.Join(root, "Gemfile.lock")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove Gemfile.lock: %v", err)
	}
	commitAll(t, root, "remove Gemfile.lock")

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected API request before build preflight: %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))
	app.Docker = &fakeDocker{digest: "sha256:unused"}

	err := app.Deploy(context.Background(), DeployOptions{NonInteractive: true})
	if err == nil {
		t.Fatal("Deploy() error = nil, want Gemfile.lock failure")
	}
	if !strings.Contains(err.Error(), "Gemfile.lock not found") {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func TestDeployFailsFastWhenWorkspaceDirty(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGit(t, root, "add", config.GenericFilePath)
	runGit(t, root, "commit", "-m", "add config")
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected API request before dirty-worktree preflight: %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))

	err := app.Deploy(context.Background(), DeployOptions{
		Image:          "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NonInteractive: true,
	})
	if err == nil {
		t.Fatal("Deploy() error = nil, want dirty-worktree failure")
	}
	if !strings.Contains(err.Error(), "git worktree has uncommitted changes") {
		t.Fatalf("Deploy() error = %v", err)
	}
	if !strings.Contains(err.Error(), "?? notes.txt") {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func TestDeployFailsWhenExistingConfigIsUncommitted(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected API request before dirty-worktree preflight: %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))

	err := app.Deploy(context.Background(), DeployOptions{
		Image:          "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NonInteractive: true,
	})
	if err == nil {
		t.Fatal("Deploy() error = nil, want dirty-worktree failure")
	}
	if !strings.Contains(err.Error(), "workspace contains devopsellence setup files that are not committed yet") {
		t.Fatalf("Deploy() error = %v", err)
	}
	if !strings.Contains(err.Error(), "git add "+config.GenericFilePath) {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func TestWarnAboutPrebuiltImageConfigForRails(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))
	var stderr bytes.Buffer
	app.Printer = output.New(io.Discard, &stderr, false)

	cfg := config.DefaultProjectConfig("default", "ShopApp", "production")
	app.warnAboutPrebuiltImageConfig(DeployOptions{
		Image: "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}, discovery.Result{WorkspaceRoot: root}, cfg)

	text := stderr.String()
	if !strings.Contains(text, "Using --image skips the local build.") {
		t.Fatalf("warning output = %q", text)
	}
	if !strings.Contains(text, "If this image is not a Rails image, update devopsellence.yml before deploy.") {
		t.Fatalf("warning output = %q", text)
	}
}

func TestDeployRailsSecretSyncRunsConcurrently(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	project := config.DefaultProjectConfig("default", "ShopApp", "production")
	project.Services["worker"] = config.ServiceConfig{Kind: config.ServiceKindWorker, Command: []string{"bin/jobs"}}
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "master.key"), []byte("master-key-value\n"), 0o600); err != nil {
		t.Fatalf("write master.key: %v", err)
	}
	commitAll(t, root, "configure deploy state")

	bothStarted := make(chan struct{})
	releaseSecrets := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	seen := map[string]bool{}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": "ShopApp"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			return jsonResponse(t, map[string]any{"secrets": []map[string]any{}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/environments/44/secrets":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode secret payload: %v", err)
			}
			service := stringValueAny(payload["service_name"])
			mu.Lock()
			seen[service] = true
			if len(seen) == 2 {
				once.Do(func() { close(bothStarted) })
			}
			mu.Unlock()
			<-releaseSecrets
			return jsonResponse(t, map[string]any{"name": stringValueAny(payload["name"]), "service_name": service, "secret_ref": "gsm://projects/test/secrets/abc/versions/latest"}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://shop.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Deploy(context.Background(), DeployOptions{
			Image:          "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			NonInteractive: true,
		})
	}()

	select {
	case <-bothStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("secret sync did not start both requests concurrently")
	}
	close(releaseSecrets)

	if err := <-errCh; err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func TestAuthSessionRefreshesOnlyOnceForConcurrentCalls(t *testing.T) {
	t.Parallel()

	root := makeGenericRoot(t)
	var refreshCalls int
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/auth/refresh":
			refreshCalls++
			return jsonResponse(t, map[string]any{
				"access_token":  "fresh-token",
				"refresh_token": "fresh-refresh",
				"token_type":    "Bearer",
				"expires_in":    3600,
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	session := newAuthSession(app, "token", true, nil)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- session.Call(context.Background(), func(token string) error {
				if token == "token" {
					return &api.StatusError{StatusCode: http.StatusUnauthorized, Message: "invalid access token"}
				}
				if token != "fresh-token" {
					t.Errorf("token = %q, want fresh-token", token)
				}
				return nil
			})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("session.Call() error = %v", err)
		}
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
}

func TestDeployIgnoresAgentsMarkdownChanges(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGit(t, root, "add", config.GenericFilePath)
	runGit(t, root, "commit", "-m", "add config")
	if err := os.WriteFile(filepath.Join(root, agentsmd.FilePath), []byte("# local notes\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/organizations":
			return jsonResponse(t, map[string]any{"organizations": []map[string]any{{"id": 7, "name": "default", "role": "owner"}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects":
			return jsonResponse(t, map[string]any{"projects": []map[string]any{{"id": 11, "name": filepath.Base(root)}}}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/projects/11/environments":
			return jsonResponse(t, map[string]any{"environments": []map[string]any{{"id": 44, "name": "production"}}}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/projects/11/releases":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{"id": 22}), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/releases/22/publish":
			return jsonResponseWithStatus(t, http.StatusCreated, map[string]any{
				"deployment_id":  77,
				"assigned_nodes": 1,
				"public_url":     "https://generic.example.test",
			}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cli/deployments/77":
			return jsonResponse(t, map[string]any{
				"id":          77,
				"sequence":    1,
				"status":      "published",
				"environment": map[string]any{"id": 44, "name": "production"},
				"release":     map[string]any{"id": 22, "revision": "rel-1"},
				"summary": map[string]any{
					"assigned_nodes": 1,
					"pending":        0,
					"reconciling":    0,
					"settled":        1,
					"error":          0,
					"active":         false,
					"complete":       true,
					"failed":         false,
				},
				"nodes": []map[string]any{
					{"id": 1, "name": "node-a", "phase": "settled", "message": "revision healthy"},
				},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{
		Image:          "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NonInteractive: true,
	}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func newTestApp(t *testing.T, cwd string, transport http.RoundTripper) *App {
	return newTestAppWithTransport(t, cwd, transport, false)
}

func newTestAppWithDeployTarget(t *testing.T, cwd string, transport http.RoundTripper) *App {
	return newTestAppWithTransport(t, cwd, transport, true)
}

func newTestAppWithTransport(t *testing.T, cwd string, transport http.RoundTripper, supportDeployTarget bool) *App {
	t.Helper()
	store := state.New(filepath.Join(t.TempDir(), "auth.json"))
	workspaceStore := state.New(filepath.Join(t.TempDir(), "workspace.json"))
	expiresAt := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	baseURL := "https://dev.devopsellence.test"
	if err := store.Write(map[string]any{
		"access_token":  "token",
		"refresh_token": "refresh-token",
		"api_base":      baseURL,
		"expires_at":    expiresAt,
	}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !supportDeployTarget && r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/deploy_target" {
			return jsonResponseWithStatus(t, http.StatusNotFound, map[string]any{"error": "not_found"}), nil
		}
		return transport.RoundTrip(r)
	})}
	authManager := auth.New(store, baseURL, baseURL)
	authManager.Client = client
	apiClient := api.New(baseURL)
	apiClient.HTTPClient = client
	return &App{
		In:             strings.NewReader(""),
		Printer:        output.New(io.Discard, io.Discard, false),
		Auth:           authManager,
		API:            apiClient,
		State:          store,
		WorkspaceState: workspaceStore,
		ConfigStore:    config.NewStore(),
		Docker:         docker.Runner{},
		Git:            git.Client{},
		Cwd:            cwd,
		ExecutablePath: func() (string, error) { return "", errors.New("ExecutablePath not stubbed") },
		LookPath:       func(string) (string, error) { return "", exec.ErrNotFound },
		Symlink:        os.Symlink,
	}
}

type fakeDocker struct {
	digest           string
	buildPlatforms   []string
	buildTarget      string
	configDir        string
	loginRegistry    string
	updates          []string
	delay            time.Duration
	imageMetadata    docker.ImageMetadata
	imageMetadataErr error
}

func (f *fakeDocker) Installed() bool { return true }

func (f *fakeDocker) DaemonReachable() bool { return true }

func (f *fakeDocker) Login(_ context.Context, registryHost, _username, _password, configDir string) error {
	f.loginRegistry = registryHost
	f.configDir = configDir
	return nil
}

func (f *fakeDocker) WithTemporaryConfig(_ context.Context, fn func(string) error) error {
	return fn("/tmp/devopsellence-docker-test")
}

func (f *fakeDocker) BuildAndPush(_ context.Context, _contextPath, _dockerfile, target string, platforms []string, configDir string, update, _ func(string)) (string, error) {
	f.buildTarget = target
	f.buildPlatforms = append([]string(nil), platforms...)
	f.configDir = configDir
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if update != nil {
		update("building test image")
		f.updates = append(f.updates, "building test image")
	}
	return f.digest, nil
}

func (f *fakeDocker) ImageMetadata(_ context.Context, _ string) (docker.ImageMetadata, error) {
	return f.imageMetadata, f.imageMetadataErr
}

type dockerUnavailableStub struct{}

func (d *dockerUnavailableStub) Installed() bool { return true }

func (d *dockerUnavailableStub) DaemonReachable() bool { return false }

func (d *dockerUnavailableStub) Login(_ context.Context, _, _, _, _ string) error {
	panic("unexpected docker login call")
}

func (d *dockerUnavailableStub) WithTemporaryConfig(_ context.Context, fn func(string) error) error {
	panic("unexpected WithTemporaryConfig call")
}

func (d *dockerUnavailableStub) BuildAndPush(_ context.Context, _, _, _ string, _ []string, _ string, _, _ func(string)) (string, error) {
	panic("unexpected BuildAndPush call")
}

func (d *dockerUnavailableStub) ImageMetadata(_ context.Context, _ string) (docker.ImageMetadata, error) {
	panic("unexpected ImageMetadata call")
}

func makeRailsRoot(t *testing.T, moduleName string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Gemfile"), []byte("source 'https://rubygems.org'\n"), 0o644); err != nil {
		t.Fatalf("write Gemfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Gemfile.lock"), []byte("GEM\n"), 0o644); err != nil {
		t.Fatalf("write Gemfile.lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	content := "module " + moduleName + "\n  class Application < Rails::Application\n  end\nend\n"
	if err := os.WriteFile(filepath.Join(root, "config", "application.rb"), []byte(content), 0o644); err != nil {
		t.Fatalf("write application.rb: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "master.key"), []byte("test-master-key\n"), 0o600); err != nil {
		t.Fatalf("write master.key: %v", err)
	}
	return root
}

func makeGitRailsRoot(t *testing.T, moduleName string) string {
	t.Helper()
	root := makeRailsRoot(t, moduleName)
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "init")
	return root
}

func makeGenericRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	return root
}

func makeGitGenericRoot(t *testing.T) string {
	t.Helper()
	root := makeGenericRoot(t)
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "init")
	return root
}

func equalJSONMap(got any, want map[string]any) bool {
	typed, ok := got.(map[string]any)
	if !ok {
		return false
	}
	if len(typed) != len(want) {
		return false
	}
	for key, value := range want {
		if typed[key] != value {
			return false
		}
	}
	return true
}

func equalSecretPayloads(got []map[string]any, want []map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	sort.Slice(got, func(i, j int) bool {
		left := stringValueAny(got[i]["service_name"]) + "\x00" + stringValueAny(got[i]["name"])
		right := stringValueAny(got[j]["service_name"]) + "\x00" + stringValueAny(got[j]["name"])
		return left < right
	})
	sort.Slice(want, func(i, j int) bool {
		left := stringValueAny(want[i]["service_name"]) + "\x00" + stringValueAny(want[i]["name"])
		right := stringValueAny(want[j]["service_name"]) + "\x00" + stringValueAny(want[j]["name"])
		return left < right
	})
	for idx := range want {
		if !equalJSONMap(got[idx], want[idx]) {
			return false
		}
	}
	return true
}

func intValueAny(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func webService(t *testing.T, cfg *config.ProjectConfig) config.ServiceConfig {
	t.Helper()
	name, ok := cfg.PrimaryWebServiceName()
	if !ok {
		t.Fatalf("missing primary web service: %#v", cfg.Services)
	}
	service, ok := cfg.Services[name]
	if !ok {
		t.Fatalf("primary web service %q missing: %#v", name, cfg.Services)
	}
	return service
}

func releaseServicePayload(t *testing.T, release api.ReleaseCreateRequest, name string) map[string]any {
	t.Helper()
	raw, ok := release.Services[name]
	if !ok {
		t.Fatalf("release service %q missing: %#v", name, release.Services)
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("release service %q payload type = %T", name, raw)
	}
	return payload
}

func servicePayloadHTTPPort(payload map[string]any) int {
	ports, ok := payload["ports"].([]any)
	if !ok {
		return 0
	}
	for _, raw := range ports {
		port, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringValueAny(port["name"]) == "http" {
			return intValueAny(port["port"])
		}
	}
	return 0
}

func stringValueAny(value any) string {
	text, _ := value.(string)
	return text
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func jsonResponse(t *testing.T, value any) *http.Response {
	return jsonResponseWithStatus(t, http.StatusOK, value)
}

func jsonResponseWithStatus(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	buffer, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(buffer)),
	}
}

func sseResponse(t *testing.T, event string, data any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal sse data: %v", err)
	}
	body := "event: " + event + "\ndata: " + string(encoded) + "\n\n"
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, strings.TrimSpace(string(output)))
	}
}

func commitAll(t *testing.T, root, message string) {
	t.Helper()
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", message)
}
