# devopsellence vision

This document captures the design center for devopsellence: the assumptions it starts from, the invariants it tries to preserve, the tradeoffs it accepts, and the things it deliberately does not try to be.

## Thesis

devopsellence starts from a simple belief: most teams do not need a new compute abstraction. Existing virtual machines are enough. Existing containerization is enough. Existing cloud primitives such as object storage, secret managers, and container registries are enough.

The problem is not that infrastructure providers failed to invent enough abstractions. The problem is that using the primitives well still requires too much glue code, too many sharp edges, and too much operational ceremony. devopsellence aims to be that missing glue. It is a toolkit and a building block, not a new universe. The closest framing is Mitchell Hashimoto's [building block economy](https://mitchellh.com/writing/building-block-economy): choose strong primitives, compose them cleanly, avoid replacing them with a grander but leakier abstraction.

In practical terms, devopsellence is very close to "take a small compose-style application description and apply it consistently across a fleet of VMs." Today the concrete configuration is `devopsellence.yml` and the agent desired-state schema, not a literal `docker-compose.yml`, but the mental model is intentionally that simple.

## Strong opinions

- One does not need further abstraction than the VM.
- One does not need a PaaS. One needs better tooling for running applications on VMs.
- One should not be forced into a platform-owned stack for analytics, logging, metrics, databases, caching, queues, or other adjacent services.

These opinions are the foundation for everything else in this document. devopsellence should reduce toil around provisioning, deployment, secrets, ingress, and reconciliation without trying to hide the machine as the primary unit of execution.

## What devopsellence is

devopsellence is a reconciler and toolkit for running containerized applications on machines you control.

At its core:

- The agent runs on a VM.
- The agent reads desired state.
- The agent pulls images, resolves secrets, starts containers, updates ingress, and reports status.
- The agent keeps reconciling until the machine matches that desired state.

Everything else is optional convenience around that loop.

The CLI is convenience. The control plane is convenience. Hosted workflows are convenience. Those pieces matter, but they are not the essence of the system. The essence is the contract between desired state and the agent that enforces it.

devopsellence also does not try to own the rest of the application stack. It does not come with a mandatory database, cache, message queue, logging backend, metrics backend, or analytics product. Users are free to integrate with existing hosted services such as PlanetScale, or run their own supporting services on infrastructure they control. A major goal is to make that choice easy rather than replace it with a devopsellence-specific answer.

## Assumptions

- The VM is already the right unit of isolation for the target user.
- Docker-level containerization is already a sufficient packaging format.
- Most target applications are a small set of cooperating services, not a large microservice graph.
- The common case is one application environment per machine, not many unrelated tenants packed onto one server.
- Teams value debuggability and explicitness more than maximum infrastructure utilization.
- Provider-native primitives are usually better than rebuilding weaker versions of them inside devopsellence.
- Users should be able to adopt devopsellence incrementally, starting from just the agent.

These assumptions are visible in the code today. The product has a solo path that reads desired state from local files and a shared path that fetches desired state and secrets from external systems. The CLI already has a solo workflow that builds desired state locally, resolves secrets from `.env`, and writes an override file to remote nodes over SSH. That split is not accidental; it reflects the intended shape of the product.

## Invariants

- One VM runs services for one application environment only.
- A node may run a web service, an optional worker, a release task, and runtime sidecars such as Envoy or `cloudflared`, but they all belong to the same application environment.
- The agent is the mandatory runtime component. Everything else is replaceable.
- Desired state is the control surface. The agent should not need imperative per-deploy shell choreography to know what to run.
- Solo mode uses the local filesystem as the source of truth for desired state and local status artifacts.
- Shared mode should use simple external primitives: object storage for desired state, a secret manager for secrets, and a container registry for images.
- The runtime data plane should stay decoupled from the management plane as much as possible.
- Ingress desired state should be the same in solo and shared mode: hostnames, public web nodes, Envoy, and node-owned TLS. The control plane may help publish DNS, but certificate private keys should stay on the node.
- Local override must always remain possible. Operators need an escape hatch.
- The system should remain understandable with ordinary tools: SSH, Docker, files, logs, JSON, and cloud CLIs.

The "one app per VM" invariant matters most. devopsellence intentionally gives up multi-tenant packing so it can avoid an entire category of scheduler, quota, noisy-neighbor, and cross-tenant isolation complexity. A node is either unassigned or assigned to exactly one environment. That constraint is a feature.

## Solo And Shared

Solo mode is the minimal expression of devopsellence.

In solo mode:

- desired state lives on the local filesystem;
- the agent reads it directly;
- status is written back to local files;
- secrets can be resolved before the desired state ever reaches the agent;
- users can manage the state with any tool they want.

This is the composability story in its purest form. If you can write the right file to disk, you can use devopsellence. You do not need a hosted control plane to get value from the agent.

Shared mode exists to preserve the same model while moving the source of truth off the machine.

In shared mode:

- desired state belongs in object storage;
- secrets belong in a secret manager;
- images belong in a container registry;
- the agent reads and reconciles those primitives directly.

Today the repo's main shared path is GCP-shaped: Cloud Storage, Secret Manager, Artifact Registry, and control-plane-issued identity. That is an implementation of the vision, not the vision itself. The deeper idea is that shared mode should still be made of understandable building blocks rather than a proprietary all-in-one substrate.

## Tradeoffs

devopsellence makes deliberate tradeoffs.

- It chooses simplicity over maximum bin-packing efficiency.
- It chooses explicit machine boundaries over dense multi-tenancy.
- It chooses provider primitives over cross-provider abstraction layers.
- It chooses reconciliation over ad hoc deploy scripts.
- It chooses composability over lock-in to one blessed control surface.
- It chooses boring operational tools over clever internal machinery.

This means devopsellence leaves some value on the table on purpose.

- It will not squeeze the highest possible utilization out of a server fleet.
- It will not hide the fact that you still own machines, images, files, and networks.
- It will not erase differences between infrastructure providers.
- It will not make operational complexity disappear for workloads that are inherently complex.

Those are acceptable losses. The goal is not theoretical platform completeness. The goal is a shorter path from "I have a VM and a containerized app" to "this runs reliably."

## What devopsellence is not

devopsellence is not a Heroku-style dyno platform.

- It does not need a dyno abstraction.
- It does not need a hidden scheduler pretending machines do not exist.
- It does not aim to turn every workload into a generic process slot.
- It does not aim to become the owner of your application's surrounding services.

devopsellence is not a functions platform.

- It does not target request-per-invocation workloads.
- It does not treat "deploy" as "upload code and let the platform invent the runtime."

devopsellence is not Kubernetes-lite.

- It does not want pods as the core user abstraction.
- It does not want to bin-pack many tenants onto one server.
- It does not want to grow a cluster control plane, scheduler, CNI stack, or CRD ecosystem.

devopsellence is not an abstraction layer over every IaaS API.

- It should not try to out-cloud the clouds.
- It should not introduce a new abstraction on top of basic IaaS primitives just for the sake of seeming higher-level.
- It should not reimplement object storage, secret management, or registries under its own brand when native services already exist.
- It should not require users to buy into a full devopsellence-managed universe before they can adopt one piece of it.

devopsellence is not an opinionated platform bundle for the rest of your stack.

- It does not force devopsellence-specific solutions for analytics, logging, metrics, databases, caching, or queues.
- It should work equally well when you bring a hosted service, a self-hosted service, or a service you run yourself on another VM.
- It should make integration easy, not make replacement mandatory.

devopsellence is not only the CLI or the control plane.

- Those are useful product surfaces.
- They are not the irreducible core.

## Composability

A user should be able to adopt devopsellence in layers.

Layer 1:

- install the agent;
- write desired state to the local filesystem;
- let the agent reconcile it.

Layer 2:

- keep the same agent;
- move desired state, images, and secrets to remote systems;
- publish to those systems with standard APIs or custom automation.

Layer 3:

- add the CLI and control plane for better workflows, multi-user management, bootstrap flows, and hosted convenience.

This layering matters. It prevents devopsellence from becoming all-or-nothing software. The low-level contract must remain useful even when the higher-level product surfaces are absent.

## Design test

A change is aligned with this vision if it makes devopsellence a better building block.

Good signs:

- less hidden machinery;
- clearer contracts;
- better solo and shared composability;
- stronger "one app per VM" boundaries;
- more leverage from existing infrastructure primitives;
- easier debugging with normal tools.

Bad signs:

- invented abstractions that hide the machine too aggressively;
- features that require multi-tenant scheduling to make sense;
- platform behavior that only works through the official control plane;
- internal systems that duplicate capabilities the cloud or the OS already provides well.

The shortest version of the vision is this:

devopsellence should make containerized applications on VMs feel operationally simple without pretending VMs, containers, files, registries, and secret stores do not exist.
