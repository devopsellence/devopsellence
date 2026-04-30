---
title: Shared overview
description: Use the same deployment model with team and control-plane workflows.
---

Shared mode keeps the same node agent, app config, and deploy verbs while
moving ownership and coordination into a control plane.

```bash
devopsellence init --mode shared
devopsellence provider login hetzner --token "$HCLOUD_TOKEN"
devopsellence node create prod-1 --provider hetzner
devopsellence deploy
devopsellence status
```

The selected workspace mode decides how root verbs behave. In shared mode,
`node create` provisions the server and runs the registration install command so
the node can pull releases and report status.

Use shared mode when you need:

- browser auth;
- organizations, projects, and environments;
- team workflows and API tokens;
- hosted deploy APIs;
- managed or self-hosted control-plane workflows.

The managed path starts at [www.devopsellence.com](https://www.devopsellence.com).
The self-hosted control plane lives in the repository `control-plane/` component.
