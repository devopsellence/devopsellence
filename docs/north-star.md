# devopsellence north star

This document turns [vision.md](vision.md) into a more concrete target. The vision explains what devopsellence believes. This document describes the system shape, architectural boundaries, and core features the product should converge on.

## North-star statement

devopsellence should be the simplest serious system for running a small-to-medium containerized application on VMs you control.

Its core job is narrow:

- interpret application config;
- validate and plan a release;
- decide what should run on which nodes;
- publish desired state;
- let the node agent reconcile that state;
- report enough status and diagnostics to explain reality.

The same deploy model should work in solo and shared mode. Solo and shared are different management topologies, not different deployment systems.

## Design priorities

- one common deployment core;
- the node agent as the only mandatory runtime component;
- desired state as the stable control surface;
- mode-independent runtime semantics;
- placement as policy, not schema;
- provider primitives instead of devopsellence-owned replacements;
- thin product surfaces around a strong core;
- ordinary tools still useful for debugging and operations.

## Target architecture

The long-term architecture should separate five concerns cleanly.

### 1. domain model

One domain model should define the core entities and invariants:

- workspace or application scope;
- environments;
- services and one-shot tasks;
- nodes and node labels;
- environment-to-node attachments;
- releases or deploy snapshots;
- desired-state publications;
- runtime status snapshots;
- ingress intent;
- secret references.

This layer should define what is valid, what can transition, and what data must exist for deploy behavior to be deterministic.

### 2. deployment core

One common deployment core should own deploy semantics:

- config interpretation;
- validation;
- planning;
- placement evaluation;
- desired-state generation;
- release selection;
- republish rules;
- ingress modeling;
- status interpretation.

This core should live in Go and be usable both in-process and over an RPC boundary. If a behavior changes deploy correctness in solo and shared, it belongs here.

### 3. state adapters

Persistence should sit behind explicit state adapters.

- solo can use sqlite or local state files;
- shared can use postgres;
- both should preserve the same logical model wherever practical.

Differences in tenancy, locking, jobs, and coordination should live in adapters, not in separate deploy semantics.

### 4. infrastructure adapters

Infrastructure integration should sit behind adapters that understand provider primitives, not product semantics.

Infrastructure adapter responsibilities include:

- container registries;
- desired-state storage;
- secret storage and resolution;
- identities and registry auth;
- compute or node provisioning where applicable;
- DNS, certificates, and cloud-owned ingress helpers;
- lifecycle cleanup and diagnostics.

Infrastructure adapters should be cloud-specific when that produces a better result. The goal is not a weak lowest-common-denominator abstraction. The goal is a clean seam between deploy semantics and infrastructure execution.

### 5. product surfaces

The CLI and control plane should stay thin relative to the core.

- the CLI should run the common core in-process for solo workflows;
- the control plane should call the same core through APIs or RPC for shared workflows;
- auth, orgs, billing, quotas, support tooling, and UI can live outside the deployment core.

Product surfaces matter, but they should not redefine the deploy model.

## System shape

The intended shape is:

```text
solo:
  cli -> deployment core -> state adapter + infrastructure adapter -> node agent

shared:
  control plane -> core api/rpc -> deployment core -> state adapter + infrastructure adapter -> node agent
```

The node agent should stay mode-agnostic. It should know how to fetch desired state, resolve secrets, pull images, reconcile containers and Envoy, and publish status through concrete adapters. It should not branch on solo or shared as product concepts.

## Core runtime model

The runtime model should stay small and explicit.

- A deployment is made of named environments, services, and tasks.
- A service is an explicit runtime unit; do not hard-code the world into one `web` and one `worker`.
- A node may carry one or more environment instances. Whether shared allows one environment per node is a policy choice, not a runtime-model constraint.
- A release should be an immutable deploy snapshot derived from config, build inputs, ingress intent, and secret references.
- Desired-state publication should be the per-node materialization of that release.
- Status should be an observed-state report tied to a release or publication revision.
- Ingress intent should live in desired state, not in a separate hidden control path.
- Secret values should live in secret stores or local operator-controlled sources; the core should mainly track references, wiring, and delivery semantics.

## Core features

These are the foundational features the system should be excellent at before it grows outward.

### Config to release

devopsellence should take `devopsellence.yml`, validate it, normalize it, and produce an auditable release snapshot with minimal ambiguity.

### Multi-service application model

The product should handle real applications with:

- multiple named web and worker services;
- release or migration tasks;
- per-environment overrides;
- health checks, ports, and runtime env;
- explicit service identity.

### Placement and attachments

The system should make node targeting explicit.

- environments attach to nodes through clear records and policies;
- placement constraints should be understandable and explain failures;
- solo and shared should use the same conceptual model even when policy differs.

### Desired-state publication

Publishing desired state should be durable, auditable, and mode-independent.

- solo should be able to publish through local artifacts;
- shared should be able to publish through object storage and service APIs;
- the node agent facing document shape should remain stable.

### Reconciliation

The node agent should continuously reconcile the node toward desired state:

- image pull and auth;
- secret resolution;
- container lifecycle;
- Envoy config and reload;
- drift correction;
- status reporting.

This loop is the heart of devopsellence.

### Secrets

Secrets should feel consistent even when the backing store changes.

- solo should support local operator-controlled secrets;
- shared should support secret-manager-backed references;
- application config should not need a different mental model per mode.

### Ingress and TLS

Ingress should be first-class and mode-independent.

- hostnames;
- public service selection;
- Envoy-managed routing;
- node-owned certificate material;
- optional DNS or cloud ingress assistance where useful.

### Status and diagnostics

Operators should be able to answer:

- what release is intended?
- what publication reached each node?
- what is actually running?
- what failed?
- what should I inspect next?

That requires durable release metadata, per-node status, and plain diagnostic surfaces rather than hidden orchestration state.

### Escape hatches

devopsellence should remain operable with ordinary tools.

- local override remains possible;
- SSH, Docker, files, logs, JSON, and cloud CLIs remain valid debugging tools;
- the official control plane should not be the only way to recover or understand a deployment.

## What should stay out of the core

These may matter to the product, but they are not the north star of the system itself:

- billing and monetization;
- account and org management;
- support and admin surfaces;
- proprietary replacements for databases, caches, queues, logs, or analytics;
- hidden schedulers or cluster abstractions;
- cross-cloud lowest-common-denominator abstractions;
- features that only make sense through the hosted control plane.

Those concerns can exist at the edge. They should not distort the deployment core.

## Sequencing bias

When tradeoffs appear, bias toward this order:

1. strengthen the shared deploy model;
2. make solo and shared semantics converge;
3. stabilize the desired-state contract and node agent adapter seams;
4. keep product shells thin;
5. add provider-specific capabilities through adapters;
6. only then expand outward into richer workflow and product layers.

## Success test

This north star is being met if the following become true:

- the same app model works in solo and shared without semantic drift;
- moving from local to hosted changes adapters more than it changes concepts;
- the release, publication, reconcile, and status path is understandable end to end;
- provider-specific integrations do not leak into the core runtime model;
- the node agent remains small, explicit, and mode-agnostic;
- operators can debug the system without learning a devopsellence-only universe.

The shortest version is this: devopsellence should become a clear, durable deployment core for containerized applications on VMs, with thin control surfaces around it and no unnecessary new abstraction layer.
