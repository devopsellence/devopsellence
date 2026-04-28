package discovery

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Result struct {
	WorkspaceRoot   string
	ProjectName     string
	ProjectSlug     string
	InferredWebPort int
}

func Discover(startDir string) (Result, error) {
	root, err := findWorkspaceRoot(startDir)
	if err != nil {
		return Result{}, err
	}
	projectName := filepath.Base(root)

	return Result{
		WorkspaceRoot:   root,
		ProjectName:     projectName,
		ProjectSlug:     Slugify(projectName),
		InferredWebPort: inferWebPort(root),
	}, nil
}

func Slugify(value string) string {
	text := strings.TrimSpace(strings.ReplaceAll(value, "::", "-"))
	if text == "" {
		return "app"
	}

	var parts []rune
	for idx, r := range text {
		if idx > 0 && isBoundary(rune(text[idx-1]), r) {
			parts = append(parts, '-')
		}
		switch {
		case r >= 'A' && r <= 'Z':
			parts = append(parts, r+'a'-'A')
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			parts = append(parts, r)
		default:
			if len(parts) == 0 || parts[len(parts)-1] == '-' {
				continue
			}
			parts = append(parts, '-')
		}
	}

	slug := strings.Trim(string(parts), "-")
	if slug == "" {
		return "app"
	}
	return slug
}

func isBoundary(prev, current rune) bool {
	if !(prev >= 'a' && prev <= 'z' || prev >= '0' && prev <= '9' || prev >= 'A' && prev <= 'Z') {
		return false
	}
	if current >= 'A' && current <= 'Z' {
		return true
	}
	return false
}

func findWorkspaceRoot(startDir string) (string, error) {
	current, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	original := current
	fallback := ""

	for {
		if configRoot(current) {
			return current, nil
		}
		if fallback == "" && workspaceCandidate(current) {
			fallback = current
		}
		parent := filepath.Dir(current)
		if parent == current {
			if fallback != "" {
				return fallback, nil
			}
			return original, nil
		}
		current = parent
	}
}

func configRoot(path string) bool {
	if _, err := os.Stat(filepath.Join(path, "devopsellence.yml")); err == nil {
		return true
	}
	return false
}

func workspaceCandidate(path string) bool {
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(path, "Dockerfile")); err == nil {
		return true
	}
	return false
}

func inferWebPort(root string) int {
	data, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		return 0
	}
	return firstExposePort(string(data))
}

func firstExposePort(text string) int {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 6 || !strings.EqualFold(trimmed[:6], "EXPOSE") {
			continue
		}
		for _, field := range strings.Fields(trimmed[6:]) {
			candidate := strings.TrimSpace(field)
			candidate = strings.TrimSuffix(candidate, "/tcp")
			candidate = strings.TrimSuffix(candidate, "/udp")
			if port, err := strconv.Atoi(candidate); err == nil && port > 0 {
				return port
			}
		}
	}
	return 0
}
