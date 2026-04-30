---
title: Deploy with shared
description: Shared mode deployment flow and node registration.
---

Shared mode keeps the same root verbs as solo, but the control plane owns
organization, project, environment, release, token, and node coordination.

```bash
devopsellence init --mode shared
devopsellence node create prod-1 --provider hetzner
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
```

Register an existing server when the VM already exists:

```bash
devopsellence node register
```

By default, registration generates a token scoped to the current environment.
Run the output command on the server to install the node agent and attach the
node.

Shared mode is the right default when API tokens, browser auth, team workflows,
and hosted/self-hosted coordination are part of the product requirement.
