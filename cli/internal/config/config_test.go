package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"schema_version: 5",
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
		"schema_version: 5",
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
		"schema_version: 5",
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
	if err == nil || !strings.Contains(err.Error(), "cannot unmarshal !!str") || !strings.Contains(err.Error(), "[]string") {
		t.Fatalf("expected string command type error, got %v", err)
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
		"schema_version: 5",
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
		"schema_version: 5",
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
