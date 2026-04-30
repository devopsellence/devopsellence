---
title: devopsellence docs
description: Agent-primary deployment docs for containerized apps on VMs.
template: splash
hero:
  tagline: Agent-primary deployments on VMs you control.
  actions:
    - text: Quickstart
      link: /getting-started/solo-quickstart/
      icon: right-arrow
---

devopsellence helps AI coding and operations agents deploy containerized apps
without inventing production shell choreography.

The contract is narrow: inspect, plan, apply desired state, observe
reconciliation, and recover with ordinary tools when needed. Humans stay in the
approval loop; the node agent stays deterministic.

## Start here

- [Solo quickstart](/getting-started/solo-quickstart/) for the shortest path to
  one VM.
- [Runtime model](/concepts/runtime-model/) for desired state, releases,
  services, nodes, and status.
- [CLI reference](/reference/cli/) for the agent-safe command surface.

Solo and shared use the same runtime model. Mode changes ownership,
persistence, and transport; it should not change deployment semantics.
