---
title: Runtime model
description: The core objects devopsellence uses to make deployments deterministic.
---

devopsellence has one deployment model across solo and shared mode.

The core objects are:

- **Application config**: `devopsellence.yml` in the app root.
- **Environment**: a named deployment target such as `staging` or `production`.
- **Service**: a named runtime unit with image, command, ports, environment, and
  health check.
- **Task**: a one-shot command, such as a release migration.
- **Node**: a VM running the devopsellence node agent.
- **Release**: an immutable deploy snapshot derived from config and build inputs.
- **Desired state**: the per-node document the node agent reconciles.
- **Status**: observed runtime state reported by the node.
- **Ingress intent**: hostnames, routes, TLS mode, and HTTP redirect behavior.

Placement is policy, not runtime schema. Solo can co-host environments on a node
when that is useful. Shared may choose stricter policies for isolation, quotas,
and operational clarity.
