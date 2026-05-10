---
title: CLI commands
description: Common devopsellence command groups.
---

The CLI is AI-operator-first: commands should emit structured results for AI
operators and use explicit plan/apply boundaries.

## Workspace

```bash
devopsellence init --mode solo
devopsellence init --mode shared
devopsellence context show
```

## Deploy

```bash
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
devopsellence logs web --node prod-1 --lines 100
devopsellence release rollback --dry-run <release-id>
```

## Nodes

```bash
devopsellence node create prod-1 --host 203.0.113.10 --user root --ssh-key ~/.ssh/id_ed25519
devopsellence node create prod-1 --provider hetzner --install --attach
devopsellence node register
devopsellence node attach prod-1
devopsellence node detach prod-1
devopsellence node diagnose prod-1
devopsellence node logs prod-1 --lines 100
```

## Node agent

```bash
devopsellence agent install prod-1
devopsellence agent uninstall prod-1 --yes
```

## Secrets

```bash
devopsellence secret set DATABASE_URL --service web --stdin
devopsellence secret list
```

## Ingress

```bash
devopsellence ingress set --service web --host app.example.com --tls-email ops@example.com
devopsellence ingress check --wait 5m
```

## Agent skills

```bash
devopsellence vibe my-app
devopsellence vibe my-app --ai-agent=codex --idea="A tiny CRM"
devopsellence vibe my-app --agent-effort=default --idea="A tiny CRM"
devopsellence vibe my-app --projects-dir ~/Work/apps --idea="A tiny CRM"
devopsellence vibe my-app --deploy-goal=dry-run --server=hetzner --server-target=prod-1 --domain=app.example.com --tls-email=ops@example.com
devopsellence vibe my-app --services=managed-postgres,object-storage,email,cloudflare-dns --idea="A tiny CRM"
devopsellence vibe my-app --idea="A tiny CRM" --no-agent
devopsellence vibe my-app --ai-agent=claude --idea="A tiny CRM" --no-launch
devopsellence skill list
devopsellence skill install
devopsellence skill install --global
devopsellence skill install --dir .agents/skills
devopsellence skill install rails-app --dir .agents/skills
```
