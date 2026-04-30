---
title: GitHub Actions
description: Deploy from CI in shared mode.
---

GitHub Actions deployment is a shared-mode workflow. CI should use an API token
and the same root CLI commands a local operator uses.

Create a deploy token:

```bash
devopsellence auth token create --name github-actions
```

Add the returned token as a repository secret (for example `DEVOPSELLENCE_TOKEN`), along with any app secrets the workflow needs.

Example workflow:

```yaml
name: Deploy

on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - name: Install devopsellence
        run: curl -fsSL https://www.devopsellence.com/lfg.sh | bash
      - name: Deploy
        env:
          DEVOPSELLENCE_TOKEN: ${{ secrets.DEVOPSELLENCE_TOKEN }}
        run: |
          ~/.local/bin/devopsellence deploy
          ~/.local/bin/devopsellence status
```

Prefer dry-run jobs for protected environments:

```bash
devopsellence deploy --dry-run
```
