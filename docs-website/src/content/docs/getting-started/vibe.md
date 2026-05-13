---
title: Build an app with vibe
description: Use devopsellence vibe to create a native-web Go app that an AI agent can build, test, and deploy.
---

`devopsellence vibe` creates an agent-ready app workspace. It is for the moment
when you have an app idea and want an AI coding agent to turn it into a
deployable web app without choosing a frontend framework, package manager, or
build system.

```bash
curl -fsSL https://www.devopsellence.com/lfg.sh | bash
~/.local/bin/devopsellence vibe my-app --idea "A tiny CRM for solo consultants"
cd ~/devopsellence-projects/my-app
codex "Read .agents/prompts/devopsellence-vibe.md and follow it."
```

The command writes a small Go web app, a Dockerfile, `devopsellence.yml`, an
initial git commit, and project-local agent skills. The generated prompt tells
the agent what to build and where the boundaries are.

## What it creates

The generated app starts with:

- Go `net/http` handlers and `html/template` pages.
- SQLite storage.
- A neutral root page and `/healthz`; no idea-specific placeholder CRUD.
- Semantic HTML, one CSS file, and no frontend build step.
- A Dockerfile with a `test` target.
- `scripts/dev`, `scripts/smoke`, and `scripts/check`.
- `devopsellence.yml` with the default web service, health check, and `/data`
  volume.
- `.agents/skills/devopsellence-app`, the app-building skill used by the agent.
- `.agents/skills/devopsellence`, the deploy and operations skill.
- `.agents/prompts/devopsellence-vibe.md`, the prompt to hand to the agent.

## Agent loop

The intended loop is:

1. The agent reads `.agents/prompts/devopsellence-vibe.md`.
2. The agent uses `.agents/skills/devopsellence-app/SKILL.md` to shape and build
   the product.
3. The app stays native-web: Go, SQLite, HTML, CSS, and small vanilla JavaScript.
4. The agent runs `docker build --target test .` while changing app behavior.
5. The agent keeps Docker and `devopsellence.yml` deploy-ready as the app grows.
6. Before adding product behavior, the agent deletes or rewrites generated shell
   code, routes, content, styles, and tests that do not serve the idea.
7. After each feature slice, the agent does a subtraction pass to remove unused
   routes, styles, helpers, placeholder UI, and speculative abstractions.
8. Before a real deploy, the agent runs `devopsellence deploy --dry-run`.

`vibe` does not ask the agent to invent a stack. It gives the agent a narrow
workspace and a high-quality set of product and implementation instructions.

## Local checks

The generated app has a single readiness check:

```bash
./scripts/check
```

If Go is installed locally, `./scripts/check` also runs `go test ./...`; without
local Go, Docker remains the portable test/build path. The agent may use these
during local iteration:

```bash
./scripts/dev
./scripts/smoke
```

`./scripts/dev` uses `go run .`, so it is a local-Go convenience. The portable
deploy-readiness contract is still `./scripts/check`.

`./scripts/check` requires Docker, runs the Docker test/build targets, and runs a
`devopsellence deploy --dry-run` when the CLI is available. If no server is
selected yet, the expected result is an explicit no-node/no-attachment blocker,
not an unset mode or invalid config error.

## Deploy

When the app is ready:

```bash
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
```

For solo mode, attach a node first if the workspace does not already have one:

```bash
devopsellence node create prod-1 --provider hetzner --install --attach
```

Use [Deploy with solo](/guides/solo-deploy/) for the full node setup and deploy
flow.

## When to use it

Use `vibe` for new small-to-medium web apps where you want:

- an AI agent to do the implementation work;
- source code that remains easy to inspect and edit;
- no frontend framework or JavaScript build pipeline;
- a deploy path to ordinary VMs with devopsellence.

Start from an existing app instead when you already have a working codebase.
Install the deploy skill with `devopsellence skill install` and ask the agent to
deploy that app.
