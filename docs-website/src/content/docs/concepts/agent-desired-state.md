---
title: Agent and desired state
description: Why the node agent is the heart of devopsellence.
---

The node agent is the mandatory runtime component. It runs on a VM, reads desired
state, and continuously reconciles reality toward that state.

The agent is responsible for:

- pulling or loading images;
- resolving secrets through configured adapters;
- starting and stopping containers;
- configuring Envoy ingress;
- rotating managed runtime artifacts;
- reporting status and diagnostics.

Desired state is the stable write boundary. AI agents, humans, CLIs, APIs, and
control planes should publish desired state through explicit contracts instead of
inventing live production shell choreography.

The node agent should stay mode-agnostic. It should be wired with concrete
adapters for source, secret resolution, status, and registry auth rather than
branching on product concepts such as solo or shared.
