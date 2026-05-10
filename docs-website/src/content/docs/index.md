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

Why care today: it gives Codex, Claude, or a human operator a narrow deployment
contract for Dockerized apps on VMs. Plan a change, deploy it, verify status and
HTTPS, inspect logs, manage secrets, and roll back with structured evidence
instead of guesses.

The contract stays narrow: inspect, plan, apply desired state, observe
reconciliation, and recover with ordinary tools when needed. Humans stay in the
approval loop; the node agent stays deterministic.

## Start here

Already have a Dockerized app:

```bash
curl -fsSL https://www.devopsellence.com/lfg.sh | bash
~/.local/bin/devopsellence skill install --global
cd my-app
codex e "Deploy this app with devopsellence solo."
```

`devopsellence skill install` installs the matching AI agent skill from the CLI
itself.

- [Solo quickstart](/getting-started/solo-quickstart/) for the shortest path to
  one VM.
- [Ingress and TLS](/guides/ingress-tls/) for hostnames, DNS checks, and HTTPS
  verification.
- [Basecamp Fizzy on Rails](/examples/fizzy-rails-solo/) for a real Rails app
  example that maps a Kamal-style deployment to devopsellence solo.
- [Flue agents on Node.js](/examples/flue-node-solo/) for deploying an
  experimental Flue webhook agent server as an ordinary containerized service.
- [AI operator model](/concepts/agent-primary/) for the product thesis: a CLI
  that gives AI operators structured feedback, safe boundaries, and facts they
  can compose with the user's tools.
- [Runtime model](/concepts/runtime-model/) for desired state, releases,
  services, nodes, and status.
- [CLI reference](/reference/cli/) for the AI-operator-safe command surface.

Solo and shared use the same runtime model. Mode changes ownership,
persistence, and transport; it should not change deployment semantics.
