---
title: Deploy with solo
description: Node setup, dry-runs, deployment, and inspection for solo mode.
---

Solo deploy scope comes from nodes attached to the current workspace and
environment.

```bash
devopsellence node attach prod-1
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
```

The CLI builds locally, transfers or loads the image for each node, writes
desired state, and lets the node agent reconcile.

Useful inspection commands:

```bash
devopsellence doctor
devopsellence node diagnose prod-1
devopsellence logs --node prod-1 --lines 100
devopsellence node logs prod-1 --lines 100
devopsellence support bundle --output ./devopsellence-support.json
```

`support bundle` writes a local, redacted JSON evidence file with workspace
config, solo state shape, attached nodes, CLI version, and recommended follow-up
commands.

To create a Hetzner-backed solo node:

```bash
devopsellence provider login hetzner --token "$HCLOUD_TOKEN"
devopsellence node create prod-1 --provider hetzner --install --attach
devopsellence doctor
```
