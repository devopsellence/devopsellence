# AGENTS.md - devopsellence monorepo

Always write "devopsellence" in all lowercase.

## Product Direction

Read [`docs/vision.md`](docs/vision.md) and [`docs/north-star.md`](docs/north-star.md) for product and architecture intent.

Critical defaults:

- devopsellence targets containerized apps on VMs; do not introduce PaaS/Kubernetes-lite abstractions.
- Desired state is the stable control surface; agent reconciliation is the core loop.
- Solo/shared are management topologies, not separate deployment systems.
- Prefer one common Go deployment core for config interpretation, validation, planning, desired-state generation, ingress, placement, and status interpretation.
- CLI should call the common core in-process for solo; Rails should eventually call it through service/RPC for shared.
- Rails owns product state: accounts, authz, billing, hosted persistence, API surfaces.
- Agent runtime should stay mode-agnostic; wire concrete adapters for desired-state source, secret resolver, status sink, registry auth, etc.
- Placement is policy, not runtime schema. Shared may require one environment per node; solo may allow multiple environments per node.
- Provider-specific integration belongs behind infrastructure adapters; do not leak cloud/provider concepts into the core runtime model.
- Desired-state/status payloads backed by protobuf use protobuf JSON casing; Rails-owned JSON/API payloads use snake_case.
- Product intent: one shared stable release version. Use `DEVOPSELLENCE_STABLE_VERSION`; do not add per-component stable version env vars or defaults.
- Keep ordinary-tool escape hatches: SSH, Docker, files, logs, JSON, cloud CLIs.

## Layout

| Path | Stack | Purpose |
|---|---|---|
| `agent/` | Go | Single-node reconciler: desired state, Docker, Envoy, cloudflared, status. |
| `cli/` | Go | `devopsellence` CLI: login, deploy, secrets, nodes, solo/shared workflows. |
| `control-plane/` | Rails 8 | Web/API app: tenants, deployments, nodes, GCP/standalone resources. |
| `test/e2e/` | Ruby + shell | Root-owned integration harness across agent, CLI, control plane, GCP mock. |
| `test/support/gcp-mock/` | Go | Local emulator for GCP APIs used by hermetic e2e tests. |

Each component still owns its tests and CI, while repo-level `mise` now declares the shared Go and Ruby toolchains used across the monorepo. There is still no shared build system.

## Commands

Use `mise`.

```sh
mise run test:agent
mise run test:cli
mise run test:cp
mise run e2e-shared
mise run e2e-solo
mise run test:all
mise run build:agent
mise run build:cli
mise run fmt:agent
```

Per component:

```sh
cd agent && mise run build && mise run test && mise run fmt
cd agent && mise run protoc
cd cli && mise run build && mise run test
cd control-plane && mise run test
cd control-plane && bin/dev
```

Control-plane tests start Postgres via mise. Single file:

```sh
cd control-plane && mise run test -- test/path/file_test.rb
```

## Code Conventions

- Go: `gofmt`; no extra linter config.
- For `agent/` and `cli/` API/doc inspection, prefer `go doc` and `gopls` before ad-hoc text searches.
- Rails: `.rubocop.yml`; prefer Rails 8 solid stack, no-build CSS/JS, Tailwind, sqlite where appropriate.
- Rails migrations are append-only. Once committed/pushed, do not edit; add a new migration.
- Never test static pages with no business logic.
- Keep local override files and machine-specific templates out of this repo.

## Key Paths

- `agent/cmd/devopsellence/`: agent entrypoint.
- `agent/internal/`: engine, reconcile, envoy, gcp, auth, cloudflared, etc.
- `agent/proto/`: desired-state protobuf.
- `cli/cmd/devopsellence/`: CLI entrypoint.
- `cli/internal/`: api, app, auth, docker, git, workflow, ui, solo, etc.
- `control-plane/app/`: Rails models, controllers, services, views, jobs.
- `control-plane/script/`: local operational scripts only.
- `.github/workflows/`: public CI/release workflows with path filters.

## Review Handling

- Explicit user product direction overrides reviewer suggestions.
- If user says no legacy / no backward compat / clean slate, do not add backfills, shims, compat code, or rejection guards just to preserve or explicitly refute removed behavior; remove the deleted surface cleanly and simplify the codepath instead.
- Open PRs as ready for review, not draft, unless the user explicitly asks for a draft.
- After addressing a PR review thread, resolve the thread in GitHub so only remaining actionable feedback stays open.
- Right after each PR is opened or updated with pushed fixes, request a fresh Copilot review with `gh pr edit <pr-number> --add-reviewer copilot-pull-request-reviewer`.

## Public Boundary

This repo is public.

- Keep source, tests, CI, and local dev tasks here.
- Keep secrets, private identifiers, tenant data, live credentials, cloud project IDs out.
- Keep private deploy infra, publishing, maintainer live e2e, prod apply/console/log/ssh, runtime-env secret sync, CDN cache busting out.
