package diagnose

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/devopsellence/devopsellence/agent/internal/version"
)

const logTailLines = 40

type Result struct {
	CollectedAt  string      `json:"collected_at"`
	AgentVersion string      `json:"agent_version"`
	Summary      Summary     `json:"summary"`
	Containers   []Container `json:"containers"`
}

type Summary struct {
	Status       string `json:"status"`
	Total        int    `json:"total"`
	Running      int    `json:"running"`
	Stopped      int    `json:"stopped"`
	Unhealthy    int    `json:"unhealthy"`
	LogsIncluded int    `json:"logs_included"`
}

type Container struct {
	Name            string            `json:"name"`
	Service         string            `json:"service,omitempty"`
	System          string            `json:"system,omitempty"`
	Image           string            `json:"image"`
	Hash            string            `json:"hash,omitempty"`
	Running         bool              `json:"running"`
	Health          string            `json:"health,omitempty"`
	HasHealthcheck  bool              `json:"has_healthcheck"`
	PublishHostPort bool              `json:"publish_host_port"`
	NetworkIPs      map[string]string `json:"network_ips,omitempty"`
	LogTail         string            `json:"log_tail,omitempty"`
}

type SnapshotCollector interface {
	Collect(ctx context.Context) (Result, error)
}

type Collector struct {
	engine engine.Engine
	now    func() time.Time
}

func NewCollector(eng engine.Engine) *Collector {
	return &Collector{
		engine: eng,
		now:    time.Now,
	}
}

func (c *Collector) Collect(ctx context.Context) (Result, error) {
	states, err := c.engine.ListManaged(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("list managed containers: %w", err)
	}
	sort.Slice(states, func(i, j int) bool {
		left := firstNonEmpty(states[i].Service, states[i].System, states[i].Name)
		right := firstNonEmpty(states[j].Service, states[j].System, states[j].Name)
		if left != right {
			return left < right
		}
		return states[i].Name < states[j].Name
	})

	result := Result{
		CollectedAt:  c.now().UTC().Format(time.RFC3339),
		AgentVersion: version.String(),
		Containers:   make([]Container, 0, len(states)),
	}

	for _, state := range states {
		info, err := c.engine.Inspect(ctx, state.Name)
		if err != nil {
			return Result{}, fmt.Errorf("inspect container %s: %w", state.Name, err)
		}

		entry := Container{
			Name:            state.Name,
			Service:         state.Service,
			System:          state.System,
			Image:           state.Image,
			Hash:            state.Hash,
			Running:         info.Running,
			Health:          info.Health,
			HasHealthcheck:  info.HasHealthcheck,
			PublishHostPort: info.PublishHostPort,
			NetworkIPs:      info.NetworkIP,
		}

		if entry.Running {
			result.Summary.Running++
		} else {
			result.Summary.Stopped++
		}
		if entry.Health == "unhealthy" {
			result.Summary.Unhealthy++
		}
		if shouldIncludeLogs(entry) {
			logs, err := c.engine.Logs(ctx, state.Name, logTailLines)
			if err == nil {
				entry.LogTail = strings.TrimSpace(string(logs))
				if entry.LogTail != "" {
					result.Summary.LogsIncluded++
				}
			}
		}

		result.Containers = append(result.Containers, entry)
	}

	result.Summary.Total = len(result.Containers)
	result.Summary.Status = summaryStatus(result.Summary)
	return result, nil
}

func shouldIncludeLogs(container Container) bool {
	return !container.Running || container.Health == "unhealthy"
}

func summaryStatus(summary Summary) string {
	if summary.Total == 0 {
		return "empty"
	}
	if summary.Unhealthy > 0 {
		return "error"
	}
	if summary.Stopped > 0 {
		return "degraded"
	}
	return "ok"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
