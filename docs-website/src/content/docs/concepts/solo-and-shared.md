---
title: Solo and shared
description: How the two management topologies differ without changing deploy semantics.
---

Solo and shared are management topologies, not separate deployment systems.

## Solo

Solo is the smallest useful expression of devopsellence:

- desired state lives in local state and files;
- SSH reaches the node;
- secrets are local values or local references;
- status is inspectable with local commands and ordinary tools.

Use solo when one human or trusted AI operator owns the deployment loop.

## Shared

Shared moves coordination out of the local machine:

- releases, projects, environments, and nodes belong to a control plane;
- images, desired state, and secrets use shared infrastructure primitives;
- API tokens and team workflows become possible;
- the node agent still reconciles desired state.

Use shared when coordination, audit, auth, or teams matter more than the shortest
local path.
