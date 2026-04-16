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
	project.Worker = &Service{
		Command:    "./bin/jobs",
		Env:        map[string]string{"QUEUE": "default"},
		SecretRefs: []SecretRef{{Name: "API_KEY", Secret: "gsm://projects/test/secrets/api-key"}},
		Volumes:    []Volume{{Source: "app_storage", Target: "/rails/storage"}},
	}

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
	if loaded.Worker == nil || loaded.Worker.Command != "./bin/jobs" {
		t.Fatalf("worker config mismatch: %#v", loaded.Worker)
	}
	if written.Build.Context != DefaultBuildContext || written.Web.Port != DefaultWebPort || written.Web.Healthcheck == nil || written.Web.Healthcheck.Path != DefaultHealthcheckPath {
		t.Fatalf("defaults missing from written config: %#v", written)
	}
	if strings.Join(loaded.Build.Platforms, ",") != strings.Join(DefaultBuildPlatforms, ",") {
		t.Fatalf("build platforms = %#v, want %#v", loaded.Build.Platforms, DefaultBuildPlatforms)
	}
	if loaded.App.Type != AppTypeRails {
		t.Fatalf("app.type = %q, want rails", loaded.App.Type)
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
		"schema_version: 3",
		"organization: acme",
		"project: ShopApp",
		"default_environment: production",
		"build:",
		"  context: .",
		"  dockerfile: Dockerfile",
		"web:",
		"  port: 3000",
		"  healthcheck:",
		"    path: /up",
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
		"schema_version: 3",
		"organization: acme",
		"project: ShopApp",
		"default_environment: production",
		"build:",
		"  context: .",
		"  dockerfile: Dockerfile",
		"web:",
		"  port: 3000",
		"  healthcheck:",
		"    path: /up",
		"init:",
		"  command: ./bin/bootstrap",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "init has been removed") {
		t.Fatalf("expected legacy init error, got %v", err)
	}
}

func TestValidateRejectsWorkerHealthcheck(t *testing.T) {
	t.Parallel()

	project := DefaultProjectConfig("acme", "ShopApp", "production")
	project.Worker = &Service{
		Command: "./bin/jobs",
		Healthcheck: &HTTPHealthcheck{
			Path: "/up",
			Port: 3000,
		},
	}

	err := Validate(&project)
	if err == nil || !strings.Contains(err.Error(), "worker cannot define port or healthcheck settings") {
		t.Fatalf("expected worker healthcheck validation error, got %v", err)
	}
}

func TestWriteAndLoadReleaseCommand(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := DefaultProjectConfig("acme", "ShopApp", "staging")
	project.ReleaseCommand = "bundle exec rails db:migrate"

	if _, err := Write(root, project); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	loaded, err := LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadFromRoot() returned nil config")
	}
	if loaded.ReleaseCommand != "bundle exec rails db:migrate" {
		t.Fatalf("release_command = %q", loaded.ReleaseCommand)
	}
}

func TestLegacyDirectNodeLabelsMigrateToSoloRoles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := DefaultProjectConfig("acme", "ShopApp", "production")
	project.LegacyDirect = &LegacyDirectConfig{Nodes: map[string]LegacyDirectNode{
		"prod-1": {
			Host:   "203.0.113.10",
			User:   "root",
			Labels: []string{NodeRoleWeb, NodeRoleWorker, NodeRoleWeb},
		},
	}}
	if _, err := Write(root, project); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	loaded, err := LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded.LegacyDirect != nil {
		t.Fatalf("direct config should be migrated away")
	}
	roles := loaded.Nodes["prod-1"].Roles
	if strings.Join(roles, ",") != "web,worker" {
		t.Fatalf("roles = %#v, want web,worker", roles)
	}
	if roles := loaded.Solo.Nodes["prod-1"].Roles; strings.Join(roles, ",") != "web,worker" {
		t.Fatalf("runtime roles = %#v, want web,worker", roles)
	}
}

func TestLegacyDirectNodeUnlabeledMigrateToAllRoles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := DefaultProjectConfig("acme", "ShopApp", "production")
	project.LegacyDirect = &LegacyDirectConfig{Nodes: map[string]LegacyDirectNode{
		"prod-1": {Host: "203.0.113.10", User: "root"},
	}}
	if _, err := Write(root, project); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	loaded, err := LoadFromRoot(root)
	if err != nil {
		t.Fatalf("LoadFromRoot() error = %v", err)
	}
	if loaded.LegacyDirect != nil {
		t.Fatalf("direct config should be migrated away")
	}
	if roles := loaded.Nodes["prod-1"].Roles; strings.Join(roles, ",") != "web,worker" {
		t.Fatalf("legacy roles = %#v, want web,worker", roles)
	}
}

func TestValidateRejectsUnknownNodeRole(t *testing.T) {
	t.Parallel()

	project := DefaultProjectConfig("acme", "ShopApp", "production")
	project.Nodes = map[string]NodeConfig{"prod-1": {Roles: []string{"db"}}}
	err := Validate(&project)
	if err == nil || !strings.Contains(err.Error(), "unsupported role") {
		t.Fatalf("expected unsupported role validation error, got %v", err)
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
	project.Web.Port = 8080
	project.Web.Healthcheck.Path = "/"
	project.Web.Healthcheck.Port = 8080

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
