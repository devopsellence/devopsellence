# Product split execution

Solo and devopsellence are separate product surfaces that share one technical
core.

This split is product and market positioning first. It is not a repo split and
not a runtime fork. Solo should stay in this repository until the shared
deployment core, node-agent contract, release model, and adapter boundaries are
stable enough for versioned dependency management.

## Product boundary

Solo is the local operator product:

- one human or trusted AI agent;
- local CLI, local state, SSH, and directly owned VMs;
- strong dry-run, status, doctor, logs, secrets, rollback, and recovery loops;
- no primary CI/CD, team coordination, hosted audit, or server-side deploy
  locking promise.

devopsellence is the company product:

- medium and large companies;
- internal containerized applications;
- GCP-native infrastructure primitives;
- control-plane-owned teams, auth, projects, environments, releases, audit
  trails, API tokens, deploy locks, and CI/CD workflows;
- PaaS-level ergonomics without hiding Compute Engine VMs, Artifact Registry,
  Secret Manager, Cloud Storage, IAM, DNS, logs, files, Docker, or JSON.

## Technical invariant

Solo and devopsellence must keep one deployment model:

- one config interpretation path;
- one validation and planning core;
- one release and desired-state publication model;
- one ingress and TLS model;
- one status interpretation model;
- one product-agnostic node-agent runtime.

The boundary belongs in adapters and product surfaces:

- solo adapters use local state, local files, SSH, and operator-controlled
  secrets;
- devopsellence adapters use Rails/Postgres plus GCP-backed desired state,
  secrets, images, identity, and status sinks.

## Naming rules

Public docs should not describe solo as a mode of devopsellence.

Use:

- "solo" for the local operator product;
- "devopsellence" for the company product;
- "devopsellence company workflows" when text must distinguish the company path
  from solo inside one CLI or doc page;
- "product surface" for user-facing boundaries;
- "adapter" for implementation boundaries.

Avoid in public positioning:

- "solo and shared are management topologies";
- "shared mode" as the company product name;
- "pick a mode";
- "mode-independent" when "product-independent" is clearer.

Implementation flags and compatibility surfaces may keep `--mode shared` until a
deliberate CLI migration exists. When that flag appears in docs, surrounding
copy should explain that it selects the devopsellence company workflow rather
than naming a second product.

## Execution sequence

1. Keep the monorepo and shared core.
2. Rename public docs routes and navigation from shared-mode language to
   devopsellence product language.
3. Add redirects for renamed docs routes.
4. Audit CLI help, errors, JSON descriptions, and generated docs for old
   shared-mode language.
5. Audit Rails/control-plane views, API docs, jobs, and admin copy for old
   shared-mode language.
6. Define the GCP-native company MVP in terms of primitives: Compute Engine,
   Artifact Registry, Secret Manager, Cloud Storage, IAM, DNS, logs, audit, CI
   deploy tokens, deploy locks, node registration, and status.
7. Only consider a repo split when solo can depend on a versioned core/agent
   package without frequent lockstep edits.

## Open follow-ups

- Decide whether the CLI should gain a product-facing alias such as
  `devopsellence init --product devopsellence` while preserving
  `devopsellence init --mode shared`.
- Add automated docs redirects for any future renamed pages.
- Decide whether examples should continue to center solo or whether the docs
  home should lead with the company product and link solo as the local operator
  path.
- Turn the GCP-native company MVP into an implementation roadmap.
