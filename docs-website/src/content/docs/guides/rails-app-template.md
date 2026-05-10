---
title: Rails app template
description: Start a production-minded Rails app with devopsellence, mise, and a local AI-agent skill.
---

`devopsellence vibe` creates one blessed Rails app shape instead of asking you
to choose a stack. It uses the devopsellence Rails template, writes a local
agent skill, seeds a prompt, initializes git, and can launch Codex, Claude Code,
Pi, or another agent with high effort/thinking enabled and a simple autonomy
choice.

Run it without `--idea` to use the intake wizard. You can press Ctrl+C during
the questions to stop before files are generated.

```bash
devopsellence vibe my-crm
```

```bash
devopsellence vibe my-crm \
  --ai-agent=codex \
  --idea="A tiny CRM for solo consultants"
cd ~/devopsellence-projects/my-crm
```

Bare app names land under `~/devopsellence-projects`. Pass `./my-crm` or an
absolute path when the app should be created somewhere else. Override the base
directory with `--projects-dir` or `DEVOPSELLENCE_PROJECTS_DIR`.

## Deployment Intake

The wizard captures deploy intent before the agent starts:

- first workflow the agent should build;
- agent freedom: builder, careful, or full-access;
- build only, prepare for solo deploy, dry-run, or deploy after approval;
- solo now, shared later, or decide later;
- no server yet, an existing server, or a Hetzner node;
- domain and TLS email;
- external service plans such as managed Postgres, object storage, email, and
  Cloudflare DNS.

This intent is written to `.agents/devopsellence-vibe.json` and summarized in
`.agents/prompts/devopsellence-vibe.md`. Tokens and secret values are never
written there.

The default autonomy is `builder`: the agent can edit files and run local
build/test commands, but the prompt still tells it to ask before secrets, paid
infrastructure, DNS changes, production deploys, destructive git commands, or
data deletion. `careful` asks more often. `full-access` starts Codex or Claude
without sandbox/approval prompts and is only appropriate inside an isolated VM,
container, or disposable devbox.

Prepare for a Hetzner-backed solo deploy without starting the agent:

```bash
devopsellence vibe my-crm \
  --idea="A tiny CRM for solo consultants" \
  --server=hetzner \
  --server-target=prod-1 \
  --deploy-goal=dry-run \
  --domain=crm.example.com \
  --tls-email=ops@example.com \
  --services=managed-postgres,cloudflare-dns \
  --no-agent
```

If Hetzner auth is missing, the prompt tells the agent to stop before
provisioning and ask the user to run:

```bash
devopsellence provider login hetzner --token <token>
```

Prepare the app and prompt without starting an agent:

```bash
devopsellence vibe my-crm --idea="A tiny CRM for solo consultants" --no-agent
```

Pin a template release when reproducing a scaffold:

```bash
devopsellence vibe my-crm --template-version=v0.1.3 --no-agent
```

Use the agent's own configured effort instead of the devopsellence default:

```bash
devopsellence vibe my-crm --ai-agent=codex --agent-effort=default
```

Start Claude with full local access inside an isolated devbox:

```bash
devopsellence vibe my-crm --ai-agent=claude --autonomy=full-access
```

The command runs Rails with the pinned template:

```bash
rails new my-crm \
  -d postgresql \
  -m https://raw.githubusercontent.com/devopsellence/rails-app-template/v0.1.3/template.rb
```

## Included Baseline

- Rails 8.1, Ruby 3.4+, PostgreSQL, Puma, Thruster, Hotwire, Turbo, Stimulus,
  ERB, importmap, Propshaft, and Tailwind.
- Solid Queue, Solid Cache, and Solid Cable.
- Active Storage ready for S3-compatible object storage.
- Rails authentication with `bcrypt`, Pundit authorization, ViewComponent,
  Pagy, lucide icons, Sentry, and OpenTelemetry.
- Minitest, Capybara system tests, Brakeman, bundler-audit, and
  rubocop-rails-omakase.
- Dockerfile, health check, `devopsellence.yml`, `.mise.toml`, and
  `.agents/skills/devopsellence-rails-app`.

The generated app uses `mise` for tool versions. The template owns the app's
Ruby and Node versions so local development, CI, and agent work start from the
same baseline.

## Agent Loop

Codex prompts start with `/goal`. Other agents get the same build-test-deploy
loop in plain prompt form. The prompt tells the agent to use:

- `.agents/skills/devopsellence-rails-app` for Rails product work;
- `.agents/skills/devopsellence` for deploys, secrets, logs, status, rollback,
  and node operations.

Use the same app shape from 0 to 1 and from 1 to a medium-company scale: add web
nodes, split workers, move PostgreSQL to managed or dedicated infrastructure,
and separate Solid Queue/Cache/Cable databases or pools when pressure appears.

## Existing Rails Apps

Install the Rails app skill into an existing repo:

```bash
devopsellence skill install rails-app --dir .agents/skills
```

Install the base devopsellence operations skill:

```bash
devopsellence skill install --dir .agents/skills
```
