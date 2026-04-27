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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devopsellence/cli/internal/api"
	"github.com/devopsellence/cli/internal/auth"
	"github.com/devopsellence/cli/internal/docker"
	"github.com/devopsellence/cli/internal/git"
	"github.com/devopsellence/cli/internal/output"
	"github.com/devopsellence/cli/internal/state"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
)

func TestInitWritesConfigOnly(t *testing.T) {
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

	if _, err := os.Stat(filepath.Join(root, config.FilePath)); err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); err == nil {
		t.Fatal("AGENTS.md exists, want not exist")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat AGENTS.md: %v", err)
	}
}

func TestInitLeavesExistingAgentsFileAlone(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	path := filepath.Join(root, "AGENTS.md")
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
	if text != existing {
		t.Fatalf("AGENTS.md = %q, want unchanged", text)
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
	if len(loaded.Ingress.Rules) != 2 {
		t.Fatalf("ingress.rules = %#v, want two host rules", loaded.Ingress.Rules)
	}
	if got, want := loaded.Ingress.Rules[0].Target.Service, config.DefaultWebServiceName; got != want {
		t.Fatalf("ingress.rules[0].target.service = %q, want %q", got, want)
	}
	if got, want := loaded.Ingress.Rules[0].Target.Port, "http"; got != want {
		t.Fatalf("ingress.rules[0].target.port = %q, want %q", got, want)
	}
	if got, want := loaded.Ingress.Rules[0].Match.Host, "www.prod-abc.devopsellence.io"; got != want {
		t.Fatalf("ingress.rules[0].match.host = %q, want %q", got, want)
	}
	if got, want := loaded.Ingress.Rules[1].Match.Host, "prod-abc.devopsellence.io"; got != want {
		t.Fatalf("ingress.rules[1].match.host = %q, want %q", got, want)
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

	var stdout bytes.Buffer
	app.Printer = output.New(&stdout, io.Discard)
	app.Auth.OpenURL = func(value string) error {
		t.Fatalf("OpenURL(%q) should not run in agent-primary JSON mode", value)
		return nil
	}

	if err := app.EnvironmentOpen(context.Background(), EnvironmentOpenOptions{}); err != nil {
		t.Fatalf("EnvironmentOpen() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["url"] != "https://shop.example.test" {
		t.Fatalf("url = %v, want https://shop.example.test", payload["url"])
	}
}

func TestConfigResolvePrintsResolvedEnvironmentConfig(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	project := config.DefaultProjectConfig("default", "ShopApp", "production")
	project.Ingress = &config.IngressConfig{
		Hosts: []string{"app.example.test"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "app.example.test", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: "web", Port: "http"},
		}},
	}
	project.Environments = map[string]config.EnvironmentOverlay{
		"staging": {
			Ingress: &config.IngressConfigOverlay{
				Hosts: []string{"staging.example.test"},
				Rules: []config.IngressRuleConfig{{
					Match:  config.IngressMatchConfig{Host: "staging.example.test", PathPrefix: "/"},
					Target: config.IngressTargetConfig{Service: "web", Port: "http"},
				}},
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
	app.Printer = output.New(&stdout, io.Discard)

	if err := app.NodeBootstrap(context.Background(), NodeBootstrapOptions{Unassigned: true}); err != nil {
		t.Fatalf("NodeBootstrap() error = %v", err)
	}
	if _, ok := captured["environment_id"]; ok {
		t.Fatalf("environment_id unexpectedly present: %#v", captured)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["assignment_mode"] != "unassigned" {
		t.Fatalf("assignment_mode = %v, want unassigned", payload["assignment_mode"])
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
	app.Printer = output.New(&stdout, io.Discard)

	if err := app.Status(context.Background(), StatusOptions{}); err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	warning, _ := payload["warning"].(string)
	if !strings.Contains(warning, "devopsellence node register") {
		t.Fatalf("warning = %q, want register hint", warning)
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

func TestNodeAssignRequiresExplicitNodeID(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request without explicit node ID: %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))

	err := app.NodeAssign(context.Background(), NodeAssignOptions{})
	if err == nil {
		t.Fatal("NodeAssign() error = nil, want node id error")
	}
	if !strings.Contains(err.Error(), "node id required") {
		t.Fatalf("NodeAssign() error = %v", err)
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
	payload := decodeJSONOutput(t, &stdout)
	if intValueAny(payload["id"]) != 55 || intValueAny(payload["environment_id"]) != 44 {
		t.Fatalf("payload = %#v, want node and environment IDs", payload)
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
	payload := decodeJSONOutput(t, &stdout)
	if intValueAny(payload["id"]) != 55 || payload["managed"] != true {
		t.Fatalf("payload = %#v, want managed node", payload)
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
	payload := decodeJSONOutput(t, &stdout)
	if intValueAny(payload["id"]) != 55 || payload["managed"] != true {
		t.Fatalf("payload = %#v, want managed node", payload)
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
	payload := decodeJSONOutput(t, &stdout)
	if intValueAny(payload["id"]) != 55 || payload["managed"] != false {
		t.Fatalf("payload = %#v, want customer-managed node", payload)
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
	if err := app.SecretSet(context.Background(), SecretSetOptions{ServiceName: " web ", Name: "SECRET_KEY_BASE", Value: "super-secret", ValueProvided: true}); err != nil {
		t.Fatalf("SecretSet() error = %v", err)
	}
	if serviceName := stringValueAny(captured["service_name"]); serviceName != "web" {
		t.Fatalf("service_name = %v, want web", captured["service_name"])
	}
	if value := stringValueAny(captured["value"]); value != "super-secret" {
		t.Fatalf("value = %v, want super-secret", captured["value"])
	}
	cfg, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	refs := cfg.Services["web"].SecretRefs
	if len(refs) != 1 || refs[0].Name != "SECRET_KEY_BASE" || refs[0].Secret != "gsm://projects/test/secrets/abc/versions/latest" {
		t.Fatalf("secret refs = %#v", refs)
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
	project := config.DefaultProjectConfig("default", "ShopApp", "staging")
	web := project.Services["web"]
	web.SecretRefs = []config.SecretRef{
		{Name: "SECRET_KEY_BASE", Secret: "gsm://projects/test/secrets/abc/versions/latest"},
		{Name: "ONLY_IN_CONFIG", Secret: "gsm://projects/test/secrets/config-only/versions/latest"},
	}
	project.Services["web"] = web
	if _, err := config.Write(root, project); err != nil {
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
	payload := decodeJSONOutput(t, &stdout)
	secrets := jsonArrayFromMap(t, payload, "secrets")
	seen := map[string]map[string]any{}
	for _, value := range secrets {
		item := jsonMapFromAny(t, value)
		seen[stringValueAny(item["name"])] = item
	}
	for name, want := range map[string]map[string]any{
		"SECRET_KEY_BASE":  {"configured": true, "stored": true, "exposed": true, "store": "managed"},
		"ONLY_IN_CONFIG":   {"configured": true, "stored": false, "exposed": true, "store": "managed"},
		"RAILS_MASTER_KEY": {"configured": false, "stored": true, "exposed": false, "store": "managed"},
	} {
		item := seen[name]
		if item == nil {
			t.Fatalf("secret %s missing from %#v", name, secrets)
		}
		for key, expected := range want {
			if item[key] != expected {
				t.Fatalf("secret %s %s = %#v, want %#v", name, key, item[key], expected)
			}
		}
	}
}

func TestSecretDeleteUsesWorkspaceEnvironment(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	project := config.DefaultProjectConfig("default", "ShopApp", "staging")
	web := project.Services["web"]
	web.SecretRefs = []config.SecretRef{{Name: "SECRET_KEY_BASE", Secret: "gsm://projects/test/secrets/abc/versions/latest"}}
	project.Services["web"] = web
	if _, err := config.Write(root, project); err != nil {
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
	if err := app.SecretDelete(context.Background(), SecretDeleteOptions{ServiceName: " web ", Name: "SECRET_KEY_BASE"}); err != nil {
		t.Fatalf("SecretDelete() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["name"] != "SECRET_KEY_BASE" || payload["service_name"] != "web" || payload["config_updated"] != true {
		t.Fatalf("payload = %#v, want deleted secret JSON", payload)
	}
	cfg, err := config.LoadFromRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if refs := cfg.Services["web"].SecretRefs; len(refs) != 0 {
		t.Fatalf("secret refs = %#v, want none", refs)
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
	payload := decodeJSONOutput(t, &stdout)
	if payload["alias_path"] != aliasPath || payload["created"] != true {
		t.Fatalf("payload = %#v, want alias JSON", payload)
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
	payload := decodeJSONOutput(t, &stdout)
	nodes := jsonArrayFromMap(t, payload, "nodes")
	if len(nodes) != 3 {
		t.Fatalf("nodes = %#v, want 3", nodes)
	}
	first := jsonMapFromAny(t, nodes[0])
	if intValueAny(first["id"]) != 8 || first["name"] != "node-a" {
		t.Fatalf("first node = %#v", first)
	}
	environment := jsonMapFromAny(t, first["environment"])
	if environment["project_name"] != "ShopApp" || environment["name"] != "production" {
		t.Fatalf("environment = %#v", environment)
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
	payload := decodeJSONOutput(t, &stdout)
	request := jsonMapFromAny(t, payload["request"])
	if intValueAny(request["id"]) != 41 || request["status"] != "completed" {
		t.Fatalf("request = %#v, want completed request #41", request)
	}
	result := jsonMapFromAny(t, request["result"])
	summary := jsonMapFromAny(t, result["summary"])
	if summary["status"] != "degraded" {
		t.Fatalf("summary = %#v, want degraded", summary)
	}
	containers := jsonArrayFromMap(t, result, "containers")
	container := jsonMapFromAny(t, containers[0])
	if container["log_tail"] != "boot failed" {
		t.Fatalf("container = %#v, want log tail", container)
	}
}

func TestNodeDiagnoseFailedStatusReturnsExitErrorAfterPrintingJSON(t *testing.T) {
	t.Parallel()

	root := makeRailsRoot(t, "ShopApp")
	var stdout bytes.Buffer
	app := newTestApp(t, root, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cli/nodes/8/diagnose_requests":
			return jsonResponseWithStatus(t, http.StatusAccepted, map[string]any{
				"id":            42,
				"status":        "failed",
				"requested_at":  "2026-03-29T20:00:00Z",
				"completed_at":  "2026-03-29T20:00:02Z",
				"error_message": "docker unavailable",
				"node":          map[string]any{"id": 8, "name": "node-a", "organization_id": 7},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))
	app.Printer.Out = &stdout

	err := app.NodeDiagnose(context.Background(), NodeDiagnoseOptions{NodeID: 8, Wait: 2 * time.Second})
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("NodeDiagnose() error = %#v, want ExitError code 1", err)
	}
	if !strings.Contains(err.Error(), "docker unavailable") {
		t.Fatalf("NodeDiagnose() error = %v, want request error message", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	request := jsonMapFromAny(t, payload["request"])
	if intValueAny(request["id"]) != 42 || request["status"] != "failed" {
		t.Fatalf("request = %#v, want failed request #42", request)
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
	app.Printer = output.New(&stdout, io.Discard)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if progressCalls < 2 {
		t.Fatalf("progressCalls = %d, want at least 2", progressCalls)
	}
	payload := decodeJSONOutput(t, &stdout)
	rollout := jsonMapFromAny(t, payload["rollout"])
	summary := jsonMapFromAny(t, rollout["summary"])
	if summary["settled"] != float64(2) || summary["complete"] != true {
		t.Fatalf("rollout summary = %#v, want complete rollout", summary)
	}
	if payload["release_id"] != float64(22) || payload["deployment_id"] != float64(77) {
		t.Fatalf("payload = %#v, want release and deployment IDs", payload)
	}
	if _, ok := payload["timings"].(map[string]any); !ok {
		t.Fatalf("payload = %#v, want timings", payload)
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

func TestDeployAppliesGitHubActionEnvVarOverrides(t *testing.T) {
	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	web := project.Services[config.DefaultWebServiceName]
	web.Env = map[string]string{"FROM_CONFIG": "1"}
	project.Services[config.DefaultWebServiceName] = web
	project.Services["worker"] = config.ServiceConfig{
		Command: []string{"./bin/jobs"},
		Env:     map[string]string{"WORKER_FROM_CONFIG": "1"},
	}
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	t.Setenv(deployEnvVarsOverrideEnv, `{"all":{"RAILS_ENV":"production"},"web":{"WEB_ONLY":"true"},"worker":{"QUEUE":"critical"}}`)

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
	if _, ok := webPayload["kind"]; ok {
		t.Fatalf("web payload unexpectedly includes kind: %#v", webPayload)
	}
	if _, ok := workerPayload["kind"]; ok {
		t.Fatalf("worker payload unexpectedly includes kind: %#v", workerPayload)
	}
	if got, want := webPayload["env"], map[string]any{"FROM_CONFIG": "1", "RAILS_ENV": "production", "WEB_ONLY": "true"}; !equalJSONMap(got, want) {
		t.Fatalf("web env = %#v, want %#v", got, want)
	}
	if got, want := workerPayload["env"], map[string]any{"WORKER_FROM_CONFIG": "1", "RAILS_ENV": "production", "QUEUE": "critical"}; !equalJSONMap(got, want) {
		t.Fatalf("worker env = %#v, want %#v", got, want)
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
	app.Printer = output.New(&stdout, io.Discard)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["status"] != "scheduling" || payload["status_message"] != "booting managed node" {
		t.Fatalf("payload = %#v, want scheduling status", payload)
	}
	rollout := jsonMapFromAny(t, payload["rollout"])
	if rollout["status_message"] != "rollout settled" {
		t.Fatalf("rollout = %#v, want final rollout status", rollout)
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
	app.Printer = output.New(&stdout, io.Discard)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	if err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if progressCalls < 4 {
		t.Fatalf("progressCalls = %d, want at least 4", progressCalls)
	}
	if payload["status"] != "scheduling" || payload["status_message"] != "waiting for managed capacity" {
		t.Fatalf("payload = %#v, want initial scheduling status", payload)
	}
	rollout := jsonMapFromAny(t, payload["rollout"])
	if rollout["status_message"] != "rollout settled" {
		t.Fatalf("rollout = %#v, want final rollout status", rollout)
	}
}

func TestDeployReportsManagedCapacityFallback(t *testing.T) {
	t.Parallel()

	root := makeGitRailsRoot(t, "ShopApp")
	if _, err := config.Write(root, config.DefaultProjectConfig("default", "ShopApp", "production")); err != nil {
		t.Fatalf("write config: %v", err)
	}
	commitAll(t, root, "add config")

	const capacityError = "No managed server capacity is available in ash/cpx11 right now. Retry in a few minutes, or use your own VM/server with `devopsellence init --mode solo`."

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
	app.Printer = output.New(&stdout, io.Discard)
	app.DeployPollInterval = 5 * time.Millisecond
	app.DeployTimeout = 500 * time.Millisecond

	err := app.Deploy(context.Background(), DeployOptions{Image: "example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err == nil {
		t.Fatal("Deploy() error = nil, want managed capacity failure")
	}
	if !strings.Contains(err.Error(), capacityError) {
		t.Fatalf("Deploy() error = %q, want %q", err.Error(), capacityError)
	}
	if stdout.String() != "" {
		t.Fatalf("Deploy() output = %q, want no success JSON on failure", stdout.String())
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
	app.Printer = output.New(&stdout, io.Discard)
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
	payload := decodeJSONOutput(t, &stdout)
	timings := jsonMapFromAny(t, payload["timings"])
	if timings["build_push_seconds"] == nil || timings["control_plane_seconds"] == nil || timings["total_seconds"] == nil {
		t.Fatalf("timings = %#v, want deploy timings", timings)
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
	app.Printer = output.New(&stdout, io.Discard)

	if err := app.Delete(context.Background(), DeleteOptions{Environment: "staging"}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["name"] != "staging" || len(jsonArrayFromMap(t, payload, "customer_node_ids")) != 2 || len(jsonArrayFromMap(t, payload, "managed_node_ids")) != 1 {
		t.Fatalf("payload = %#v, want deleted environment JSON", payload)
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
	app.Printer = output.New(&stdout, &stdout)

	if err := app.TokenList(context.Background(), TokenListOptions{}); err != nil {
		t.Fatalf("TokenList() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	tokens := jsonArrayFromMap(t, payload, "tokens")
	if len(tokens) != 2 {
		t.Fatalf("tokens = %#v, want 2", tokens)
	}
	first := jsonMapFromAny(t, tokens[0])
	second := jsonMapFromAny(t, tokens[1])
	if intValueAny(first["id"]) != 10 || first["name"] != "deploy" || first["current"] != true {
		t.Fatalf("first token = %#v", first)
	}
	if intValueAny(second["id"]) != 11 || second["revoked_at"] == "" {
		t.Fatalf("second token = %#v", second)
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
	app.Printer = output.New(&stdout, &stderr)
	app.Docker = &fakeDocker{imageMetadata: docker.ImageMetadata{ExposedPorts: []int{3000}}}

	opts := DeployOptions{
		Image:          "docker.io/mccutchen/go-httpbin@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NonInteractive: true,
	}
	if err := app.Deploy(context.Background(), opts); err != nil {
		t.Fatalf("first Deploy() error = %v", err)
	}
	firstPayload := decodeJSONOutput(t, &stdout)
	if firstPayload["trial_expires_at"] != "2026-03-29T19:00:00Z" {
		t.Fatalf("first deploy payload = %#v, want trial expiry", firstPayload)
	}

	stdout.Reset()
	stderr.Reset()
	if err := app.Deploy(context.Background(), opts); err != nil {
		t.Fatalf("second Deploy() error = %v", err)
	}
	secondPayload := decodeJSONOutput(t, &stdout)
	if secondPayload["trial_expires_at"] != "2026-03-29T19:00:00Z" {
		t.Fatalf("second deploy payload = %#v, want trial expiry", secondPayload)
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
	if !strings.Contains(err.Error(), "workspace contains devopsellence init files that are not committed yet") {
		t.Fatalf("Deploy() error = %v", err)
	}
	if !strings.Contains(err.Error(), "git add "+config.GenericFilePath) {
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
	session := newAuthSession(app, "token", nil)

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

func TestDeployTreatsAgentsMarkdownAsUserChange(t *testing.T) {
	t.Parallel()

	root := makeGitGenericRoot(t)
	project := config.DefaultProjectConfigForType("default", filepath.Base(root), "production", config.AppTypeGeneric)
	if _, err := config.Write(root, project); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGit(t, root, "add", config.GenericFilePath)
	runGit(t, root, "commit", "-m", "add config")
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# local notes\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
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
	if !strings.Contains(err.Error(), "AGENTS.md") {
		t.Fatalf("Deploy() error = %v, want AGENTS.md", err)
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
		Printer:        output.New(io.Discard, io.Discard),
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
