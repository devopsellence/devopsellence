package solo

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const envFile = ".env"

func envPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, envFile)
}

// LoadSecrets reads KEY=VALUE pairs from .env in the workspace root.
// Returns an empty map if the file does not exist.
// Supports blank lines, # comments, optional quoting, and export prefix.
func LoadSecrets(workspaceRoot string) (map[string]string, error) {
	path := envPath(workspaceRoot)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", envFile, err)
	}
	defer f.Close()

	secrets := map[string]string{}
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip optional "export " prefix.
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", envFile, lineNum)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = unquote(value)
		secrets[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", envFile, err)
	}
	return secrets, nil
}

// SaveSecret sets a key in .env, preserving comments and ordering.
// Creates the file if it doesn't exist.
func SaveSecret(workspaceRoot, key, value string) error {
	path := envPath(workspaceRoot)
	lines, err := readLines(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", envFile, err)
	}

	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lineKey, _, ok := strings.Cut(strings.TrimPrefix(trimmed, "export "), "=")
		if ok && strings.TrimSpace(lineKey) == key {
			lines[i] = key + "=" + quoteIfNeeded(value)
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, key+"="+quoteIfNeeded(value))
	}

	return writeLines(path, lines)
}

// DeleteSecret removes a key from .env.
func DeleteSecret(workspaceRoot, key string) error {
	path := envPath(workspaceRoot)
	lines, err := readLines(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("secret %q not found", key)
		}
		return fmt.Errorf("read %s: %w", envFile, err)
	}

	found := false
	out := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			lineKey, _, ok := strings.Cut(strings.TrimPrefix(trimmed, "export "), "=")
			if ok && strings.TrimSpace(lineKey) == key {
				found = true
				continue
			}
		}
		out = append(out, line)
	}
	if !found {
		return fmt.Errorf("secret %q not found", key)
	}

	return writeLines(path, out)
}

// ListSecrets returns sorted key names from .env.
func ListSecrets(workspaceRoot string) ([]string, error) {
	secrets, err := LoadSecrets(workspaceRoot)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := string(data)
	if s == "" {
		return nil, nil
	}
	// Preserve original line endings; split without stripping trailing newline.
	lines := strings.Split(s, "\n")
	// Remove trailing empty element from final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

func writeLines(path string, lines []string) error {
	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o600)
}

// unquote strips matching single or double quotes from a value and
// unescapes backslash sequences inside double-quoted strings.
func unquote(s string) string {
	if len(s) >= 2 {
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return s[1 : len(s)-1]
		}
		if s[0] == '"' && s[len(s)-1] == '"' {
			inner := s[1 : len(s)-1]
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, `\\`, `\`)
			return inner
		}
	}
	return s
}

// quoteIfNeeded wraps the value in double quotes if it contains spaces,
// #, quotes, or backslashes.
func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, " \t#\"'\\") {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return `"` + s + `"`
	}
	return s
}
