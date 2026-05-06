package agentskill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallWritesBundledSkill(t *testing.T) {
	dir := t.TempDir()
	result, err := Install(dir, "v1-test")
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
