---
title: Install
description: Install the devopsellence CLI and verify the local environment.
---

Install the CLI:

```bash
curl -fsSL https://www.devopsellence.com/lfg.sh | bash
```

The installer writes to `~/.local/bin` by default. If that directory is not on
your `PATH`, it prints the shell command to add it.

devopsellence is AI-operator-first. To install the matching AI agent skill:

```bash
~/.local/bin/devopsellence skill install --global
```

Verify the workstation:

```bash
devopsellence doctor
```

You also need:

- a git checkout for the app you want to deploy;
- a Dockerfile for that app;
- Docker available locally for build workflows;
- SSH access to a Linux VM for solo mode.
