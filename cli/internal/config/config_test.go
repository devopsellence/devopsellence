package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWriteAndLoadFromRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := DefaultProjectConfig("acme", "ShopApp", "staging")
	project.Services["jobs"] = Service{
		Kind:       ServiceKindWorker,
		Command:    []string{"./bin/jobs"},
		Env:        map[string]string{"QUEUE": "default"},
		SecretRefs: []SecretRef{{Name: "API_KEY", Secret: "gsm://projects/test/secrets/api-key"}},
		Volumes:    []Volume{{Source: "app_storage", Target: "/rails/storage"}},
	}
	project.Tasks.Release = &TaskConfig{Service: "web", Command: []string{"bundle", "exec", "rails", "db:migrate"}}

	written, err := Write(root, project)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	loaded, err := LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadFromRoot() returned nil config")
	}
	if loaded.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", loaded.SchemaVersion, SchemaVersion)
	}
	if loaded.Organization != "acme" || loaded.Project != "ShopApp" || loaded.DefaultEnvironment != "staging" {
		t.Fatalf("loaded core fields mismatch: %#v", loaded)
	}
	if got := loaded.Services["jobs"].Command; len(got) != 1 || got[0] != "./bin/jobs" {
		t.Fatalf("jobs service mismatch: %#v", loaded.Services["jobs"])
	}
	if written.Build.Context != DefaultBuildContext {
		t.Fatalf("build context = %q, want %q", written.Build.Context, DefaultBuildContext)
	}
	web := written.Services[DefaultWebServiceName]
	if web.HTTPPort(0) != DefaultWebPort || web.Healthcheck == nil || web.Healthcheck.Path != DefaultHealthcheckPath {
		t.Fatalf("defaults missing from written config: %#v", web)
	}
	if strings.Join(loaded.Build.Platforms, ",") != strings.Join(DefaultBuildPlatforms, ",") {
		t.Fatalf("build platforms = %#v, want %#v", loaded.Build.Platforms, DefaultBuildPlatforms)
	}
	if loaded.Tasks.Release == nil || strings.Join(loaded.Tasks.Release.Command, " ") != "bundle exec rails db:migrate" {
		t.Fatalf("release task = %#v", loaded.Tasks.Release)
	}
	if _, err := os.Stat(filepath.Join(root, FilePath)); err != nil {
		t.Fatalf("root config missing: %v", err)
	}
}

func TestLoadRejectsSchemaWithoutRootConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, FilePath)
	if err := os.WriteFile(path, []byte("organization: acme\nproject: ShopApp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("expected schema_version error, got %v", err)
	}
}

func TestLoadAppliesDefaultBuildPlatforms(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, FilePath)
	content := strings.Join([]string{
		"schema_version: 6",
		"organization: acme",
		"project: ShopApp",
		"default_environment: production",
		"build:",
		"  context: .",
		"  dockerfile: Dockerfile",
		"services:",
		"  web:",
		"    kind: web",
		"    ports:",
		"      - name: http",
		"        port: 3000",
		"    healthcheck:",
		"      path: /up",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("Load() returned nil config")
	}
	if strings.Join(cfg.Build.Platforms, ",") != strings.Join(DefaultBuildPlatforms, ",") {
		t.Fatalf("build platforms = %#v, want %#v", cfg.Build.Platforms, DefaultBuildPlatforms)
	}
}

func TestLoadRejectsLegacyInitHook(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, FilePath)
	content := strings.Join([]string{
		"schema_version: 6",
		"organization: acme",
		"project: ShopApp",
		"default_environment: production",
		"build:",
		"  context: .",
		"  dockerfile: Dockerfile",
		"services:",
		"  web:",
		"    kind: web",
		"    ports:",
		"      - name: http",
		"        port: 3000",
		"    healthcheck:",
		"      path: /up",
		"init:",
		"  command: ./bin/bootstrap",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field init not found") {
		t.Fatalf("expected unknown init error, got %v", err)
	}
}

func TestLoadRejectsStringCommandSyntax(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, FilePath)
	content := strings.Join([]string{
		"schema_version: 6",
		"organization: acme",
		"project: ShopApp",
		"default_environment: production",
		"build:",
		"  context: .",
		"  dockerfile: Dockerfile",
		"services:",
		"  web:",
		"    kind: web",
		"    command: ./bin/server",
		"    ports:",
		"      - name: http",
		"        port: 3000",
		"    healthcheck:",
		"      path: /up",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected string command type error, got nil")
	}
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("expected yaml.TypeError, got %T (%v)", err, err)
	}
}

func TestValidateAcceptsWorkerWithoutExtraPlacementFields(t *testing.T) {
	t.Parallel()

	project := DefaultProjectConfig("acme", "ShopApp", "production")
	project.Services["jobs"] = Service{
		Kind:    ServiceKindWorker,
		Command: []string{"./bin/jobs"},
	}

	err := Validate(&project)
	if err != nil {
		t.Fatalf("expected worker service to validate, got %v", err)
	}
}

func TestLoadRejectsLegacyDirectConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, FilePath)
	content := strings.Join([]string{
		"schema_version: 6",
		"organization: acme",
		"project: ShopApp",
		"default_environment: production",
		"build:",
		"  context: .",
		"  dockerfile: Dockerfile",
		"services:",
		"  web:",
		"    kind: web",
		"    ports:",
		"      - name: http",
		"        port: 3000",
		"    healthcheck:",
		"      path: /up",
		"direct:",
		"  nodes:",
		"    prod-1:",
		"      host: 203.0.113.10",
		"      user: root",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field direct not found") {
		t.Fatalf("expected unknown direct error, got %v", err)
	}
}

func TestLoadRejectsLegacySoloConfigBlock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, FilePath)
	content := strings.Join([]string{
		"schema_version: 6",
		"organization: solo",
		"project: ShopApp",
		"default_environment: production",
		"build:",
		"  context: .",
		"  dockerfile: Dockerfile",
		"services:",
		"  web:",
		"    kind: web",
		"    ports:",
		"      - name: http",
		"        port: 3000",
		"    healthcheck:",
		"      path: /up",
		"solo:",
		"  nodes:",
		"    prod-1:",
		"      host: 203.0.113.10",
		"      user: root",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field solo not found") {
		t.Fatalf("expected unknown solo field error, got %v", err)
	}
}

func TestValidateRejectsBlankBuildPlatform(t *testing.T) {
	t.Parallel()

	project := DefaultProjectConfig("acme", "ShopApp", "production")
	project.Build.Platforms = []string{"linux/amd64", ""}

	err := Validate(&project)
	if err == nil || !strings.Contains(err.Error(), "build.platforms entries must be present") {
		t.Fatalf("expected build.platforms validation error, got %v", err)
	}
}

func TestWriteGenericConfigUsesRepoRootPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := DefaultProjectConfigForType("acme", "GenericApp", "production", AppTypeGeneric)
	web := project.Services[DefaultWebServiceName]
	web.Ports = []ServicePort{{Name: "http", Port: 8080}}
	web.Healthcheck.Path = "/"
	web.Healthcheck.Port = 8080
	project.Services[DefaultWebServiceName] = web

	if _, err := Write(root, project); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, GenericFilePath)); err != nil {
		t.Fatalf("generic config missing: %v", err)
	}
	loaded, err := LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded == nil || loaded.App.Type != AppTypeGeneric {
		t.Fatalf("loaded generic config mismatch: %#v", loaded)
	}
}

func TestReadmeExampleConfigParses(t *testing.T) {
	t.Parallel()

	readmePath := filepath.Join("..", "..", "..", "README.md")
	content, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", readmePath, err)
	}

	marker := "`devopsellence` reads `devopsellence.yml` from the app root:"
	start := strings.Index(string(content), marker)
	if start == -1 {
		t.Fatalf("README marker %q not found", marker)
	}

	section := string(content[start:])
	fenceStart := strings.Index(section, "```yaml\n")
	if fenceStart == -1 {
		t.Fatal("README yaml fence not found after example config marker")
	}
	section = section[fenceStart+len("```yaml\n"):]
	fenceEnd := strings.Index(section, "\n```")
	if fenceEnd == -1 {
		t.Fatal("README yaml closing fence not found")
	}

	root := t.TempDir()
	path := filepath.Join(root, FilePath)
	if err := os.WriteFile(path, []byte(section[:fenceEnd]+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", path, err)
	}
	if cfg == nil {
		t.Fatal("Load() returned nil config")
	}
}

func TestResolveEnvironmentConfigMergesOverlay(t *testing.T) {
	t.Parallel()

	project := DefaultProjectConfig("acme", "ShopApp", "production")
	project.Ingress = &IngressConfig{
		Hosts:   []string{"app.example.test"},
		Service: "web",
		TLS:     IngressTLSConfig{Mode: "auto", Email: "ops@example.test"},
	}
	project.Services["web"] = Service{
		Kind:       ServiceKindWeb,
		Command:    []string{"bundle", "exec", "puma"},
		Args:       []string{"-C", "config/puma.rb"},
		Env:        map[string]string{"RAILS_ENV": "production", "BASE_ONLY": "1"},
		SecretRefs: []SecretRef{{Name: "BASE_KEY", Secret: "gsm://base"}},
		Ports:      []ServicePort{{Name: "http", Port: 3000}},
		Healthcheck: &HTTPHealthcheck{
			Path: "/up",
			Port: 3000,
		},
		Volumes: []Volume{{Source: "storage", Target: "/rails/storage"}},
	}
	project.Tasks.Release = &TaskConfig{
		Service: "web",
		Command: []string{"bundle", "exec", "rails", "db:migrate"},
		Env:     map[string]string{"RELEASE_ONLY": "base"},
	}
	redirectHTTP := false
	stagingService := "web"
	stagingPath := "/healthz"
	stagingPort := 8080
	project.Environments = map[string]EnvironmentOverlay{
		"staging": {
			Ingress: &IngressConfigOverlay{
				Hosts:   []string{"staging.example.test", "alt-staging.example.test"},
				Service: &stagingService,
				TLS: &IngressTLSConfigOverlay{
					Email: stringPtr("staging@example.test"),
				},
				RedirectHTTP: &redirectHTTP,
			},
			Services: map[string]ServiceConfigOverlay{
				"web": {
					Command:     []string{"./bin/staging-web"},
					Env:         map[string]string{"RAILS_ENV": "staging", "STAGING_ONLY": "1"},
					SecretRefs:  []SecretRef{{Name: "STAGING_KEY", Secret: "gsm://staging"}},
					Ports:       []ServicePort{{Name: "http", Port: 8080}},
					Volumes:     []Volume{{Source: "staging-storage", Target: "/rails/storage"}},
					Healthcheck: &HTTPHealthcheckOverlay{Path: &stagingPath, Port: &stagingPort},
				},
			},
			Tasks: &TasksConfigOverlay{
				Release: &TaskConfigOverlay{
					Env:     map[string]string{"RELEASE_ONLY": "staging", "MIGRATION_MODE": "online"},
					Command: []string{"bundle", "exec", "rails", "db:prepare"},
				},
			},
		},
	}

	resolved, err := ResolveEnvironmentConfig(project, "staging")
	if err != nil {
		t.Fatalf("ResolveEnvironmentConfig() error = %v", err)
	}
	if resolved.DefaultEnvironment != "staging" {
		t.Fatalf("default_environment = %q, want staging", resolved.DefaultEnvironment)
	}
	if got := strings.Join(resolved.Ingress.Hosts, ","); got != "staging.example.test,alt-staging.example.test" {
		t.Fatalf("ingress.hosts = %q", got)
	}
	if resolved.Ingress.TLS.Mode != "auto" || resolved.Ingress.TLS.Email != "staging@example.test" {
		t.Fatalf("ingress tls = %#v", resolved.Ingress.TLS)
	}
	if resolved.Ingress.RedirectHTTP == nil || *resolved.Ingress.RedirectHTTP {
		t.Fatalf("ingress.redirect_http = %#v, want false", resolved.Ingress.RedirectHTTP)
	}
	web := resolved.Services["web"]
	if got := strings.Join(web.Command, " "); got != "./bin/staging-web" {
		t.Fatalf("command = %q", got)
	}
	if web.Args[0] != "-C" {
		t.Fatalf("args = %#v", web.Args)
	}
	if web.Env["RAILS_ENV"] != "staging" || web.Env["BASE_ONLY"] != "1" || web.Env["STAGING_ONLY"] != "1" {
		t.Fatalf("env = %#v", web.Env)
	}
	if len(web.SecretRefs) != 1 || web.SecretRefs[0].Name != "STAGING_KEY" {
		t.Fatalf("secret_refs = %#v", web.SecretRefs)
	}
	if web.Healthcheck == nil || web.Healthcheck.Path != "/healthz" || web.Healthcheck.Port != 8080 {
		t.Fatalf("healthcheck = %#v", web.Healthcheck)
	}
	if resolved.Tasks.Release == nil {
		t.Fatal("release task missing")
	}
	if got := strings.Join(resolved.Tasks.Release.Command, " "); got != "bundle exec rails db:prepare" {
		t.Fatalf("release command = %q", got)
	}
	if resolved.Tasks.Release.Env["RELEASE_ONLY"] != "staging" || resolved.Tasks.Release.Env["MIGRATION_MODE"] != "online" {
		t.Fatalf("release env = %#v", resolved.Tasks.Release.Env)
	}
}

func TestResolveEnvironmentConfigReturnsBaseForMissingOverlay(t *testing.T) {
	t.Parallel()

	project := DefaultProjectConfig("acme", "ShopApp", "production")
	resolved, err := ResolveEnvironmentConfig(project, "staging")
	if err != nil {
		t.Fatalf("ResolveEnvironmentConfig() error = %v", err)
	}
	if resolved.DefaultEnvironment != "staging" {
		t.Fatalf("default_environment = %q, want staging", resolved.DefaultEnvironment)
	}
	if _, ok := resolved.Services["web"]; !ok {
		t.Fatalf("resolved services = %#v", resolved.Services)
	}
}

func TestLoadRejectsOverlayServiceNotInBase(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, FilePath)
	content := strings.Join([]string{
		"schema_version: 6",
		"organization: acme",
		"project: ShopApp",
		"default_environment: production",
		"build:",
		"  context: .",
		"  dockerfile: Dockerfile",
		"services:",
		"  web:",
		"    kind: web",
		"    ports:",
		"      - name: http",
		"        port: 3000",
		"    healthcheck:",
		"      path: /up",
		"environments:",
		"  staging:",
		"    services:",
		"      jobs:",
		"        command:",
		"          - ./bin/jobs",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "environments.staging.services.jobs not found in services") {
		t.Fatalf("expected missing service error, got %v", err)
	}
}

func TestLoadRejectsUnknownOverlayKeys(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, FilePath)
	content := strings.Join([]string{
		"schema_version: 6",
		"organization: acme",
		"project: ShopApp",
		"default_environment: production",
		"build:",
		"  context: .",
		"  dockerfile: Dockerfile",
		"services:",
		"  web:",
		"    kind: web",
		"    ports:",
		"      - name: http",
		"        port: 3000",
		"    healthcheck:",
		"      path: /up",
		"environments:",
		"  staging:",
		"    build:",
		"      context: .",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field build not found") {
		t.Fatalf("expected unknown overlay key error, got %v", err)
	}
}

func stringPtr(value string) *string {
	return &value
}
