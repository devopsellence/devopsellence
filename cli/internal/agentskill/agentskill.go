package agentskill

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const Name = "devopsellence"

type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

var skills = []Skill{
	{
		ID:          "devopsellence",
		Name:        Name,
		Description: "Operate devopsellence deployments, nodes, secrets, logs, diagnostics, and rollback.",
	},
}

//go:embed all:devopsellence
var bundled embed.FS

type InstallResult struct {
	ID      string
	Name    string
	Path    string
	Paths   []InstalledPath
	Version string
	Source  string
}

type InstalledPath struct {
	Agent string
	Scope string
	Path  string
}

type InstallOptions struct {
	SkillsDir     string
	WorkspaceRoot string
	Global        bool
	Skill         string
}

type installTarget struct {
	Agent     string
	Scope     string
	SkillsDir string
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

func Available() []Skill {
	result := append([]Skill(nil), skills...)
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

func Resolve(idOrName string) (Skill, error) {
	if idOrName == "" {
		idOrName = Name
	}
	for _, skill := range skills {
		if idOrName == skill.ID || idOrName == skill.Name {
			return skill, nil
		}
	}
	return Skill{}, fmt.Errorf("unknown skill %q", idOrName)
}

func Install(opts InstallOptions, version string) (InstallResult, error) {
	skill, err := Resolve(opts.Skill)
	if err != nil {
		return InstallResult{}, err
	}
	targets, err := installTargets(opts)
	if err != nil {
		return InstallResult{}, err
	}

	installed := make([]InstalledPath, 0, len(targets))
	for _, target := range targets {
		dest := filepath.Join(target.SkillsDir, skill.Name)
		if err := installTo(dest, skill.Name); err != nil {
			return InstallResult{}, err
		}
		installed = append(installed, InstalledPath{
			Agent: target.Agent,
			Scope: target.Scope,
			Path:  dest,
		})
	}
	return InstallResult{ID: skill.ID, Name: skill.Name, Path: installed[0].Path, Paths: installed, Version: version, Source: "embedded"}, nil
}

func installTargets(opts InstallOptions) ([]installTarget, error) {
	modes := 0
	if opts.SkillsDir != "" {
		modes++
	}
	if opts.WorkspaceRoot != "" {
		modes++
	}
	if opts.Global {
		modes++
	}
	if modes == 0 {
		return nil, fmt.Errorf("missing install target: run from a devopsellence workspace, use --global, or pass --dir <path>")
	}
	if modes > 1 {
		return nil, fmt.Errorf("conflicting install targets: use only one of a devopsellence workspace, --global, or --dir <path>")
	}

	if opts.SkillsDir != "" {
		return []installTarget{{Agent: "agents", Scope: "custom", SkillsDir: opts.SkillsDir}}, nil
	}

	if opts.Global {
		defaultDir, err := DefaultSkillsDir()
		if err != nil {
			return nil, err
		}
		targets := []installTarget{{Agent: "agents", Scope: "global", SkillsDir: defaultDir}}
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		if isDir(filepath.Join(home, ".claude")) {
			targets = append(targets, installTarget{Agent: "claude", Scope: "global", SkillsDir: filepath.Join(home, ".claude", "skills")})
		}
		return targets, nil
	}

	targets := []installTarget{{Agent: "agents", Scope: "project", SkillsDir: ProjectSkillsDir(opts.WorkspaceRoot)}}
	if isDir(filepath.Join(opts.WorkspaceRoot, ".claude")) {
		targets = append(targets, installTarget{Agent: "claude", Scope: "project", SkillsDir: filepath.Join(opts.WorkspaceRoot, ".claude", "skills")})
	}
	return targets, nil
}

func installTo(dest, skillName string) error {
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove existing skill: %w", err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create skill dir: %w", err)
	}
	if err := fs.WalkDir(bundled, skillName, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(skillName, path)
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
