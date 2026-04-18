package desiredstate

import (
	"fmt"
	"regexp"
	"strings"
)

var sanitizeRe = regexp.MustCompile(`[^a-z0-9_.-]+`)

func ContainerName(serviceName, revision, hash string) (string, error) {
	service := sanitize(serviceName)
	if service == "" {
		return "", fmt.Errorf("service_name sanitizes to empty")
	}
	rev := sanitize(revision)
	if rev == "" {
		return "", fmt.Errorf("revision sanitizes to empty")
	}
	shortHash := hash
	if len(shortHash) > 8 {
		shortHash = shortHash[:8]
	}
	name := fmt.Sprintf("svc-%s-%s-%s", service, rev, shortHash)
	if len(name) > 255 {
		return "", fmt.Errorf("container name too long")
	}
	return name, nil
}

func ServiceContainerName(environmentName, serviceName, revision, hash string) (string, error) {
	env := sanitize(environmentName)
	if env == "" {
		return "", fmt.Errorf("environment name sanitizes to empty")
	}
	service := sanitize(serviceName)
	if service == "" {
		return "", fmt.Errorf("service name sanitizes to empty")
	}
	rev := sanitize(revision)
	if rev == "" {
		return "", fmt.Errorf("revision sanitizes to empty")
	}
	shortHash := hash
	if len(shortHash) > 8 {
		shortHash = shortHash[:8]
	}
	name := fmt.Sprintf("svc-%s-%s-%s-%s", env, service, rev, shortHash)
	if len(name) > 255 {
		return "", fmt.Errorf("container name too long")
	}
	return name, nil
}

func sanitize(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	clean := sanitizeRe.ReplaceAllString(lower, "-")
	clean = strings.Trim(clean, "-.")
	clean = strings.ReplaceAll(clean, "..", ".")
	clean = strings.ReplaceAll(clean, "--", "-")
	return clean
}
