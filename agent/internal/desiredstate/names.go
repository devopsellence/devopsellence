package desiredstate

import (
	"crypto/sha256"
	"encoding/hex"
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

func EnvironmentNetworkPrefix(baseNetworkName string) string {
	base := sanitize(baseNetworkName)
	if base == "" {
		base = "devopsellence"
	}
	return base + "-env-"
}

func EnvironmentNetworkName(baseNetworkName, environmentName string) (string, error) {
	env := sanitize(environmentName)
	if env == "" {
		return "", fmt.Errorf("environment name sanitizes to empty")
	}
	name := EnvironmentNetworkPrefix(baseNetworkName) + env
	if len(name) <= 255 {
		return name, nil
	}
	sum := sha256.Sum256([]byte(environmentName))
	suffix := hex.EncodeToString(sum[:4])
	maxEnvLen := 255 - len(EnvironmentNetworkPrefix(baseNetworkName)) - len(suffix) - 1
	if maxEnvLen <= 0 {
		return "", fmt.Errorf("environment network name too long")
	}
	if len(env) > maxEnvLen {
		env = env[:maxEnvLen]
		env = strings.Trim(env, "-.")
	}
	if env == "" {
		return "", fmt.Errorf("environment name sanitizes to empty")
	}
	return EnvironmentNetworkPrefix(baseNetworkName) + env + "-" + suffix, nil
}

func sanitize(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	clean := sanitizeRe.ReplaceAllString(lower, "-")
	clean = strings.Trim(clean, "-.")
	clean = strings.ReplaceAll(clean, "..", ".")
	clean = strings.ReplaceAll(clean, "--", "-")
	return clean
}
