package discovery

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	modulePattern      = regexp.MustCompile(`^\s*module\s+([A-Z][A-Za-z0-9_:]*)\b`)
	applicationPattern = regexp.MustCompile(`^\s*class\s+Application\s*<\s*Rails::Application\b`)
)

type Result struct {
	RailsRoot       string
	WorkspaceRoot   string
	AppType         string
	ProjectName     string
	ProjectSlug     string
	ModuleName      string
	FallbackUsed    bool
	InferredWebPort int
}

func Discover(startDir string) (Result, error) {
	root, appType, err := findWorkspaceRoot(startDir)
	if err != nil {
		return Result{}, err
	}

	moduleName := ""
	fallbackUsed := false
	projectName := filepath.Base(root)
	if appType == "rails" {
		moduleName, err = applicationModuleName(root)
		if err != nil {
			return Result{}, err
		}
		fallbackUsed = strings.TrimSpace(moduleName) == ""
		if !fallbackUsed {
			projectName = moduleName
		}
	}

	return Result{
		RailsRoot:       root,
		WorkspaceRoot:   root,
		AppType:         appType,
		ProjectName:     projectName,
		ProjectSlug:     Slugify(projectName),
		ModuleName:      moduleName,
		FallbackUsed:    fallbackUsed,
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

func findWorkspaceRoot(startDir string) (string, string, error) {
	current, err := filepath.Abs(startDir)
	if err != nil {
		return "", "", err
	}
	original := current
	fallback := ""

	for {
		if railsRoot(current) {
			return current, "rails", nil
		}
		if genericRoot(current) {
			return current, "generic", nil
		}
		if fallback == "" && genericWorkspaceCandidate(current) {
			fallback = current
		}
		parent := filepath.Dir(current)
		if parent == current {
			if fallback != "" {
				return fallback, "generic", nil
			}
			return original, "generic", nil
		}
		current = parent
	}
}

func railsRoot(path string) bool {
	if _, err := os.Stat(filepath.Join(path, "Gemfile")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, "config", "application.rb")); err != nil {
		return false
	}
	return true
}

func genericRoot(path string) bool {
	if _, err := os.Stat(filepath.Join(path, "devopsellence.yml")); err == nil {
		return true
	}
	return false
}

func genericWorkspaceCandidate(path string) bool {
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(path, "Dockerfile")); err == nil {
		return true
	}
	return false
}

func applicationModuleName(root string) (string, error) {
	file, err := os.Open(filepath.Join(root, "config", "application.rb"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer file.Close()

	var modules []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if match := modulePattern.FindStringSubmatch(line); len(match) == 2 {
			modules = append(modules, strings.Split(match[1], "::")...)
			continue
		}
		if applicationPattern.MatchString(line) {
			if len(modules) == 0 {
				return "", nil
			}
			return modules[len(modules)-1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read application module: %w", err)
	}
	return "", nil
}

func inferWebPort(root string) int {
	dockerfilePath := filepath.Join(root, "Dockerfile")
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return 0
	}

	text := string(data)
	if port := explicitRailsServerPort(text); port > 0 {
		return port
	}
	if usesThrust(text) {
		if port := firstExposePort(text); port > 0 {
			return port
		}
		return 80
	}
	return firstExposePort(text)
}

func explicitRailsServerPort(text string) int {
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if !strings.Contains(lower, "rails") || !strings.Contains(lower, "server") {
			continue
		}
		fields := strings.Fields(strings.NewReplacer("[", " ", "]", " ", ",", " ", "\"", " ", "'", " ").Replace(line))
		for idx := 0; idx < len(fields); idx++ {
			switch fields[idx] {
			case "-p", "--port":
				if idx+1 >= len(fields) {
					continue
				}
				if port, err := strconv.Atoi(strings.TrimSpace(fields[idx+1])); err == nil && port > 0 {
					return port
				}
			default:
				if strings.HasPrefix(fields[idx], "-p") && len(fields[idx]) > 2 {
					if port, err := strconv.Atoi(strings.TrimPrefix(fields[idx], "-p")); err == nil && port > 0 {
						return port
					}
				}
				if strings.HasPrefix(fields[idx], "--port=") {
					if port, err := strconv.Atoi(strings.TrimPrefix(fields[idx], "--port=")); err == nil && port > 0 {
						return port
					}
				}
			}
		}
	}
	return 0
}

func usesThrust(text string) bool {
	return strings.Contains(text, "bin/thrust") || strings.Contains(text, "\"thrust\"") || strings.Contains(text, "'thrust'")
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
