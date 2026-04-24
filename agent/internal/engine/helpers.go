package engine

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// WaitReady polls containerName until it is running and healthy (or the
// timeout expires). component is used only in error messages.
func WaitReady(ctx context.Context, eng Engine, containerName, component string, timeout time.Duration, requireHealthcheck bool) error {
	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if time.Now().After(deadline) {
			info, err := eng.Inspect(ctx, containerName)
			if err != nil {
				return fmt.Errorf("inspect %s: %w", component, err)
			}
			return fmt.Errorf("%s not ready after %s (running=%t health=%s)", component, timeout, info.Running, info.Health)
		}

		info, err := eng.Inspect(ctx, containerName)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", component, err)
		}
		if !info.Running {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if requireHealthcheck && !info.HasHealthcheck {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if info.Health == "" || info.Health == "healthy" {
			return nil
		}
		if info.Health == "unhealthy" {
			return fmt.Errorf("%s unhealthy", component)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// EnsureImage pulls image if it is not already present locally.
// Intended for system containers that need no registry auth.
func EnsureImage(ctx context.Context, eng Engine, image string) error {
	exists, err := eng.ImageExists(ctx, image)
	if err != nil {
		return fmt.Errorf("inspect image %s: %w", image, err)
	}
	if exists {
		return nil
	}
	if err := eng.PullImage(ctx, image, nil); err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	return nil
}

// RestartPolicyFromString converts a policy name string (e.g. "unless-stopped")
// to a RestartPolicy. Returns nil for empty / "no" / "none" / "disabled".
func RestartPolicyFromString(policy string) *RestartPolicy {
	normalized := strings.ToLower(strings.TrimSpace(policy))
	if normalized == "" || normalized == "no" || normalized == "none" || normalized == "disabled" {
		return nil
	}
	return &RestartPolicy{Name: normalized}
}
