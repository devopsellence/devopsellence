---
title: Rails app template
description: Start a production-minded Rails app with devopsellence, mise, and a local AI-agent skill.
---

`devopsellence vibe` creates one blessed Rails app shape instead of asking you
to choose a stack. It uses the devopsellence Rails template, writes a local
agent skill, seeds a prompt, initializes git, and can launch Codex, Claude Code,
Pi, or another agent.

```bash
devopsellence vibe my-crm \
  --ai-agent=codex \
  --idea="A tiny CRM for solo consultants"
```

Prepare the app and prompt without starting an agent:

```bash
devopsellence vibe my-crm --idea="A tiny CRM for solo consultants" --no-agent
```

Pin a template release when reproducing a scaffold:

```bash
devopsellence vibe my-crm --template-version=v0.1.3 --no-agent
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
