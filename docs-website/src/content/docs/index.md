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
- [Basecamp Fizzy on Rails](/examples/fizzy-rails-solo/) for a real Rails app
  example that maps a Kamal-style deployment to devopsellence solo.
- [Flue agents on Node.js](/examples/flue-node-solo/) for deploying a webhook
  AI agent server to your own VM with devopsellence solo.
- [Runtime model](/concepts/runtime-model/) for desired state, releases,
  services, nodes, and status.
- [CLI reference](/reference/cli/) for the AI-operator-safe command surface.

Solo and shared use the same runtime model. Mode changes ownership,
persistence, and transport; it should not change deployment semantics.
