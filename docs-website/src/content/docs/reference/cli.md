---
title: CLI commands
description: Common devopsellence command groups.
---

The CLI is agent-primary: commands should emit structured results and use
explicit plan/apply boundaries.

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
devopsellence logs --service web
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

## Agent

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
