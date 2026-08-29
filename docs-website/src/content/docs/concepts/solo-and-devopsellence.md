---
title: Solo and devopsellence
description: How the local operator product and company product share one runtime.
---

Solo and devopsellence are separate product surfaces that share one deployment
core and one node-agent runtime.

## Solo

Solo is the local operator product:

- desired state lives in local state and files;
- SSH reaches the node;
- secrets are local values or local references;
- status is inspectable with local commands and ordinary tools.

Use solo when one human or trusted AI operator owns the deployment loop.

## devopsellence

devopsellence moves coordination out of the local machine and into company
infrastructure:

- releases, projects, environments, and nodes belong to a control plane;
- images, desired state, secrets, and identity use GCP primitives;
- API tokens and team workflows become possible;
- the node agent still reconciles desired state.

Use devopsellence when coordination, audit, auth, GCP-native operations, or
teams matter more than the shortest local path.
