---
title: Overview
description: What devopsellence is and how it relates to solo.
---

devopsellence deploys internal containerized applications to GCP VMs. It does
not try to hide machines, containers, files, registries, identities, or secret
stores. It makes the ordinary GCP primitives easier to use together.

Solo is a separate local operator tool that shares the same deployment core. Its
value is an operator-safe local loop: dry-run, deploy, status, doctor, logs,
exec, secrets, rollback, DNS checks, and HTTPS verification with structured
output an AI assistant can act on.

The system has three product surfaces:

- **Node agent**: runs on each node and reconciles containers, Envoy ingress,
  secrets, and status.
- **CLI**: runs locally to plan, deploy, inspect, and manage solo workflows,
  and talks to devopsellence APIs for company workflows.
- **Control plane**: adds sign-in, organizations, projects, environments, tokens,
  release coordination, GCP-backed state, and team workflows for devopsellence.

## Product boundary

| | Solo | devopsellence |
| --- | --- | --- |
| Control surface | Local CLI and files | Hosted or self-hosted APIs |
| Transport | SSH | Node agent pulls published state |
| Auth | SSH keys | Browser auth and API tokens |
| Images | Streamed or loaded through SSH workflows | Artifact Registry |
| Secrets | Local operator-controlled state or references | Secret Manager-backed team secrets |
| Infrastructure | Existing Linux VMs | GCP primitives |
| Best for | One operator or trusted AI agent | Internal company apps and teams |

Solo and devopsellence should converge on the same config and deploy semantics.
They are separate product surfaces, not separate runtimes.

## What devopsellence is not

devopsellence is not a traditional PaaS, functions platform, Kubernetes-lite,
cloud API abstraction layer, or bundle for databases/caches/queues/logging.
Bring the services you want; devopsellence focuses on deploying and reconciling
your internal app while keeping GCP primitives visible.
