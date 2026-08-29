---
title: devopsellence overview
description: Use the shared deployment core with company and control-plane workflows.
---

devopsellence keeps the same node agent, app config, and deploy model as solo
while moving ownership and coordination into a control plane backed by GCP
primitives.

```bash
devopsellence init --mode shared
devopsellence provider login hetzner --token "$HCLOUD_TOKEN"
devopsellence node create prod-1 --provider hetzner
devopsellence deploy
devopsellence status
```

The selected workspace mode decides how root verbs behave while the product
positioning remains separate. In devopsellence company workflows, `node create`
provisions the server and runs the registration install command so the node can
pull releases and report status.

Use devopsellence when you need:

- browser auth;
- organizations, projects, and environments;
- team workflows and API tokens;
- hosted deploy APIs;
- GCP-backed desired state, secrets, images, and identity;
- managed or self-hosted control-plane workflows for internal company apps.

The managed path starts at [www.devopsellence.com](https://www.devopsellence.com).
The self-hosted control plane lives in the repository `control-plane/` component.
