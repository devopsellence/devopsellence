package agentsmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devopsellence/cli/internal/config"
)

const (
	FilePath    = "AGENTS.md"
	beginMarker = "<!-- devopsellence:begin -->"
	endMarker   = "<!-- devopsellence:end -->"
)

func PathFor(railsRoot string) string {
	return filepath.Join(railsRoot, FilePath)
}

func Write(railsRoot string, cfg config.ProjectConfig) (string, error) {
	path := PathFor(railsRoot)
	block := managedBlock(cfg)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	content := nextContent(string(existing), block)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func nextContent(existing, block string) string {
	if strings.TrimSpace(existing) == "" {
		return "# AGENTS.md\n\n" + block
	}

	begin := strings.Index(existing, beginMarker)
	end := strings.Index(existing, endMarker)
	if begin >= 0 && end >= begin {
		end += len(endMarker)
		return strings.TrimRight(existing[:begin], "\n") + "\n\n" + block + trailingContent(existing[end:])
	}

	return strings.TrimRight(existing, "\n") + "\n\n" + block + "\n"
}

func trailingContent(value string) string {
	trimmed := strings.TrimLeft(value, "\n")
	if trimmed == "" {
		return "\n"
	}
	return "\n\n" + trimmed
}

func managedBlock(cfg config.ProjectConfig) string {
	return strings.TrimSpace(fmt.Sprintf(`
%s
## devopsellence

This app is managed with the devopsellence CLI.

Common commands:
- `+"`devopsellence mode use solo|shared`"+`
- `+"`devopsellence context show`"+`
- `+"`devopsellence setup`"+`
- `+"`devopsellence doctor`"+`
- `+"`devopsellence deploy`"+`
- `+"`devopsellence status`"+`

Secrets:
- `+"`devopsellence secret list`"+`
- `+"`printf '%%s' \"$VALUE\" | devopsellence secret set NAME --service web --stdin`"+`
- `+"`devopsellence secret set NAME --service web --store 1password --op-ref op://vault/item/field`"+`
- `+"`devopsellence secret delete NAME --service web`"+`

Solo mode:
- `+"`devopsellence mode use solo`"+`
- `+"`devopsellence provider login hetzner`"+`
- `+"`devopsellence secret set NAME --service web --value ...`"+`
- `+"`devopsellence node list`"+`
- `+"`devopsellence node logs NODE --follow`"+`
- `+"`devopsellence node create prod-1`"+`
- `+"`devopsellence node attach prod-1`"+`

Shared mode:
- `+"`devopsellence mode use shared`"+`
- `+"`devopsellence provider login hetzner`"+`
- `+"`devopsellence node create prod-1`"+`
- `+"`devopsellence deploy --image registry.example.com/app@sha256:...`"+`
- `+"`devopsellence node register`"+`
- `+"`devopsellence node list`"+`
- `+"`devopsellence node attach <target>`"+`
- `+"`devopsellence node detach <target>`"+`
- `+"`devopsellence node remove <id>`"+`

Lifecycle hooks in `+"`devopsellence.yml`"+`:
- `+"`tasks.release`"+` runs once before rollout; use it for migrations and other release-wide one-shot work. It reuses its configured service image, env, secrets, and volumes.

Default workspace:
- Organization: %s
- Project: %s
- Environment: %s
%s
`, beginMarker, cfg.Organization, cfg.Project, cfg.DefaultEnvironment, endMarker))
}
