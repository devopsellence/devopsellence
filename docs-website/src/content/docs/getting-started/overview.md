---
title: Overview
description: What devopsellence is and when to use each mode.
---

devopsellence deploys containerized applications to VMs you control. It does not
try to hide machines, containers, files, registries, or secret stores. It makes
the ordinary primitives easier to use together.

The system has three product surfaces:

- **Agent**: runs on each node and reconciles containers, Envoy ingress, secrets,
  and status.
- **CLI**: runs locally or in CI to plan, deploy, inspect, and manage solo/shared
  workflows.
- **Control plane**: adds sign-in, organizations, projects, environments, tokens,
  release coordination, and team workflows for shared mode.

## Pick a mode

| | Solo | Shared |
| --- | --- | --- |
| Control surface | Local CLI and files | Hosted or self-hosted APIs |
| Transport | SSH | Agent pulls published state |
| Auth | SSH keys | Browser auth and API tokens |
| Images | Streamed or loaded through SSH workflows | Pushed to a registry |
| Secrets | Local operator-controlled state or references | Server-side team secret management |
| Best for | Side projects, single-dev apps, staging | Teams, production, multi-environment apps |

Both modes should converge on the same config and deploy semantics. Mode is a
management topology, not a different runtime.

## What devopsellence is not

devopsellence is not a PaaS, functions platform, Kubernetes-lite, cloud API
abstraction layer, or bundle for databases/caches/queues/logging. Bring the
services you want; devopsellence focuses on deploying and reconciling your app.
