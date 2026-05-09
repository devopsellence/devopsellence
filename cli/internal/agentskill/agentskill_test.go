package agentskill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallWritesBundledSkill(t *testing.T) {
	dir := t.TempDir()
	result, err := Install(InstallOptions{SkillsDir: dir}, "v1-test")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if result.Name != Name || result.Version != "v1-test" || result.Source != "embedded" {
		t.Fatalf("result = %#v, want bundled devopsellence skill", result)
	}
	path := filepath.Join(dir, Name, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("%s is empty", path)
	}
}

func TestInstallDefaultsToProjectAgentSkillDirs(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspaceRoot, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Install(InstallOptions{WorkspaceRoot: workspaceRoot}, "v1-test")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	agentsPath := filepath.Join(workspaceRoot, ".agents", "skills", Name, "SKILL.md")
	if _, err := os.Stat(agentsPath); err != nil {
		t.Fatalf("expected project agents skill at %s: %v", agentsPath, err)
	}
	claudePath := filepath.Join(workspaceRoot, ".claude", "skills", Name, "SKILL.md")
	if _, err := os.Stat(claudePath); err != nil {
		t.Fatalf("expected project claude skill at %s: %v", claudePath, err)
	}
	if result.Path != filepath.Join(workspaceRoot, ".agents", "skills", Name) {
		t.Fatalf("path = %q, want project agents skill path", result.Path)
	}
	if len(result.Paths) != 2 || result.Paths[0].Agent != "agents" || result.Paths[0].Scope != "project" || result.Paths[1].Agent != "claude" || result.Paths[1].Scope != "project" {
		t.Fatalf("paths = %#v, want project agents and claude targets", result.Paths)
	}
}

func TestInstallGlobalUsesUserAgentSkillDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Install(InstallOptions{Global: true}, "v1-test")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	agentsPath := filepath.Join(home, ".agents", "skills", Name, "SKILL.md")
	if _, err := os.Stat(agentsPath); err != nil {
		t.Fatalf("expected global agents skill at %s: %v", agentsPath, err)
	}
	claudePath := filepath.Join(home, ".claude", "skills", Name, "SKILL.md")
	if _, err := os.Stat(claudePath); err != nil {
		t.Fatalf("expected global claude skill at %s: %v", claudePath, err)
	}
	if result.Path != filepath.Join(home, ".agents", "skills", Name) {
		t.Fatalf("path = %q, want global agents skill path", result.Path)
	}
	if len(result.Paths) != 2 || result.Paths[0].Scope != "global" || result.Paths[1].Scope != "global" {
		t.Fatalf("paths = %#v, want global targets", result.Paths)
	}
}

func TestInstallRejectsGlobalWithExplicitDir(t *testing.T) {
	_, err := Install(InstallOptions{SkillsDir: t.TempDir(), Global: true}, "v1-test")
	if err == nil {
		t.Fatal("Install() error = nil, want --dir/--global conflict")
	}
}

func TestBundledSkillMatchesPublicSkill(t *testing.T) {
	bundled, err := os.ReadFile(filepath.Join("devopsellence", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(filepath.Join("..", "..", "..", "skills", "devopsellence", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bundled) != string(public) {
		t.Fatal("bundled devopsellence skill differs from skills/devopsellence/SKILL.md")
	}
}
