---
title: devopsellence docs
description: AI-operator-first deployment docs for containerized apps on VMs.
template: splash
hero:
  tagline: AI-operator-first deployments on vanilla VMs.
  actions:
    - text: Quickstart
      link: /getting-started/solo-quickstart/
      icon: right-arrow
---

devopsellence helps AI coding and operations assistants deploy containerized
apps without inventing production shell choreography.

The contract is narrow: inspect, plan, apply desired state, observe
reconciliation, and recover with ordinary tools when needed. Humans stay in the
approval loop; the node agent stays deterministic.

## Start here

```bash
curl -fsSL https://www.devopsellence.com/lfg.sh | bash -s -- --install-agent-skill
cd my-app
codex e "Deploy this app with devopsellence solo."
```

- [Solo quickstart](/getting-started/solo-quickstart/) for the shortest path to
  one VM.
- [Agent-first DevOps](/concepts/agent-primary/) for the product thesis: a CLI
  that gives AI operators structured feedback, safe boundaries, and facts they
  can compose with tools like Cloudflare, GitHub Actions, and secret stores.
- [Runtime model](/concepts/runtime-model/) for desired state, releases,
  services, nodes, and status.
- [CLI reference](/reference/cli/) for the AI-operator-safe command surface.

Solo and shared use the same runtime model. Mode changes ownership,
persistence, and transport; it should not change deployment semantics.
