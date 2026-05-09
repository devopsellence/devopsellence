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
	Paths   []InstallTarget
	Version string
	Source  string
}

type InstallTarget struct {
	Agent string
	Scope string
	Path  string
}

type InstallOptions struct {
	SkillsDir     string
	WorkspaceRoot string
	Global        bool
}

func DefaultSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

func ProjectSkillsDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".agents", "skills")
}

func Install(opts InstallOptions, version string) (InstallResult, error) {
	targets, err := installTargets(opts)
	if err != nil {
		return InstallResult{}, err
	}

	installed := make([]InstallTarget, 0, len(targets))
	for _, target := range targets {
		dest := filepath.Join(target.Path, Name)
		if err := installTo(dest); err != nil {
			return InstallResult{}, err
		}
		installed = append(installed, InstallTarget{
			Agent: target.Agent,
			Scope: target.Scope,
			Path:  dest,
		})
	}
	return InstallResult{Name: Name, Path: installed[0].Path, Paths: installed, Version: version, Source: "embedded"}, nil
}

func installTargets(opts InstallOptions) ([]InstallTarget, error) {
	if opts.SkillsDir != "" {
		if opts.Global {
			return nil, fmt.Errorf("cannot use --dir with --global")
		}
		return []InstallTarget{{Agent: "agents", Scope: "custom", Path: opts.SkillsDir}}, nil
	}

	if opts.Global {
		defaultDir, err := DefaultSkillsDir()
		if err != nil {
			return nil, err
		}
		targets := []InstallTarget{{Agent: "agents", Scope: "global", Path: defaultDir}}
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		if isDir(filepath.Join(home, ".claude")) {
			targets = append(targets, InstallTarget{Agent: "claude", Scope: "global", Path: filepath.Join(home, ".claude", "skills")})
		}
		return targets, nil
	}

	if opts.WorkspaceRoot == "" {
		return nil, fmt.Errorf("missing workspace root")
	}
	targets := []InstallTarget{{Agent: "agents", Scope: "project", Path: ProjectSkillsDir(opts.WorkspaceRoot)}}
	if isDir(filepath.Join(opts.WorkspaceRoot, ".claude")) {
		targets = append(targets, InstallTarget{Agent: "claude", Scope: "project", Path: filepath.Join(opts.WorkspaceRoot, ".claude", "skills")})
	}
	return targets, nil
}

func installTo(dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove existing skill: %w", err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create skill dir: %w", err)
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
		return fmt.Errorf("write bundled skill: %w", err)
	}
	return nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
