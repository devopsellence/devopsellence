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
- **Node peers**: the other attached nodes a node may need to know about for
  peer-aware runtime behavior such as multi-node ACME HTTP-01 challenge
  forwarding.

Placement is policy, not runtime schema. Solo can co-host environments on a node
when that is useful. Shared may choose stricter policies for isolation, quotas,
and operational clarity.

A useful consequence is that solo remains local-only without being single-node
only. The CLI can publish enough desired state over SSH for nodes to cooperate at
runtime, while the node agent stays mode-agnostic and reconciles the same shape
of intent in both solo and shared mode.
