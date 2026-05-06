---
title: Environment variables
description: Environment variables used by devopsellence workflows.
---

This reference starts with the public variables users commonly touch. It should
grow as CLI and release contracts stabilize.

| Variable | Use |
| --- | --- |
| `DEVOPSELLENCE_TOKEN` | Shared-mode API token for non-interactive workflows. |
| `DEVOPSELLENCE_STABLE_VERSION` | Shared stable release version for public installer/runtime surfaces. |
| `DEVOPSELLENCE_STATE_HOME` | Base directory for local CLI, workspace, solo, SSH, and provider state. Takes precedence over `XDG_STATE_HOME`. |
| `HCLOUD_TOKEN` | Hetzner token consumed by provider login examples. |
| `XDG_STATE_HOME` | Fallback base directory for local state when `DEVOPSELLENCE_STATE_HOME` is unset. |

Keep private tenant data, live credentials, cloud project IDs, and maintainer
runtime environment details out of this public docs site.
