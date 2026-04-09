package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var shaPattern = regexp.MustCompile(`\A[0-9a-f]{40}\z`)

type Client struct{}

func (Client) CurrentSHA(root string) (string, error) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("could not determine git SHA from %s. commit the app and run inside a git checkout", root)
	}
	sha := strings.TrimSpace(out.String())
	if !shaPattern.MatchString(sha) {
		return "", fmt.Errorf("could not determine git SHA from %s. commit the app and run inside a git checkout", root)
	}
	return sha, nil
}

func (Client) StatusEntries(root string, ignorePaths []string) ([]string, error) {
	args := []string{"-C", root, "status", "--porcelain", "--untracked-files=all", "--", "."}
	for _, path := range normalizedIgnorePaths(ignorePaths) {
		args = append(args, ":(exclude)"+path)
	}

	cmd := exec.Command("git", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("could not determine git status from %s. commit the app and run inside a git checkout", root)
	}

	output := strings.TrimSpace(out.String())
	if output == "" {
		return nil, nil
	}
	return strings.Split(output, "\n"), nil
}

func normalizedIgnorePaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		path = strings.TrimPrefix(path, "./")
		path = strings.TrimPrefix(path, "/")
		if path == "" || path == "." {
			continue
		}
		normalized = append(normalized, path)
	}
	slices.Sort(normalized)
	return slices.Compact(normalized)
}
