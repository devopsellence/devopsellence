package agentskill

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const Name = "devopsellence"

//go:embed all:devopsellence
var bundled embed.FS

type InstallResult struct {
	Name    string
	Path    string
	Version string
	Source  string
}

func DefaultSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

func Install(skillsDir string, version string) (InstallResult, error) {
	if skillsDir == "" {
		defaultDir, err := DefaultSkillsDir()
		if err != nil {
			return InstallResult{}, err
		}
		skillsDir = defaultDir
	}
	dest := filepath.Join(skillsDir, Name)
	if err := os.RemoveAll(dest); err != nil {
		return InstallResult{}, fmt.Errorf("remove existing skill: %w", err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("create skill dir: %w", err)
	}
	if err := fs.WalkDir(bundled, Name, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(Name, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dest, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := bundled.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}); err != nil {
		return InstallResult{}, fmt.Errorf("write bundled skill: %w", err)
	}
	return InstallResult{Name: Name, Path: dest, Version: version, Source: "embedded"}, nil
}
