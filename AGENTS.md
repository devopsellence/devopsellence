# AGENTS.md - devopsellence monorepo

Always write "devopsellence" in all lowercase.

## Layout

| Path | Stack | Purpose |
|---|---|---|
| `agent/` | Go | Single-node reconciler: desired state, Docker, Envoy, cloudflared, status. |
| `cli/` | Go | `devopsellence` CLI: login, deploy, secrets, nodes, solo/shared workflows. |
| `control-plane/` | Rails 8 | Web/API app: tenants, deployments, nodes, GCP/standalone resources. |
| `test/e2e/` | Ruby + shell | Root-owned integration harness across agent, CLI, control plane, GCP mock. |
| `test/support/gcp-mock/` | Go | Local emulator for GCP APIs used by hermetic e2e tests. |

Each component has its own toolchain, tests, and CI. No shared build system.

## Commands

Use `mise`.

From repo root:

```
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

```
cd agent && mise run build && mise run test && mise run fmt
cd agent && mise run protoc
cd cli && mise run build && mise run test
cd control-plane && mise run test
cd control-plane && bin/dev
```

Control-plane tests start Postgres via mise. Single file:

```
cd control-plane && mise run test -- test/path/file_test.rb
```

## Structure

- `agent/cmd/devopsellence/`: agent entrypoint.
- `agent/internal/`: engine, reconcile, envoy, gcp, auth, cloudflared, etc.
- `agent/proto/`: desired-state protobuf.
- `cli/cmd/devopsellence/`: CLI entrypoint.
- `cli/internal/`: api, app, auth, docker, git, workflow, ui, solo, etc.
- `control-plane/app/`: Rails models, controllers, services, views, jobs.
- `control-plane/script/`: local operational scripts only.
- `.github/workflows/`: public CI/release workflows with path filters.

## Conventions

- Go: `gofmt`; no extra linter config.
- Rails: `.rubocop.yml`.
- Prefer Rails 8 solid stack, no-build CSS/JS, Tailwind, sqlite where appropriate.
- Never test static pages with no business logic.
- Keep local override files and machine-specific templates out of this repo.

## Architecture

- Solo/shared deploy semantics should match. Differences: user/org/project management, ownership, persistence, transport, policy.
- Shared core owns config interpretation, validation, planning, desired-state generation, ingress, placement constraints, status interpretation.
- Prefer Go for reusable deployment core logic. CLI calls it in-process for solo; Rails should eventually call it through service/RPC.
- Rails owns product state: accounts, authz, billing, hosted persistence, API surfaces.
- Placement is policy, not schema. Shared may require one environment per node; solo may allow multiple environments per node.
- Desired state is mode-independent node runtime state. A node may carry multiple environment instances; an environment may have multiple named services/workers.
- Agent runtime must not know product modes like solo/shared. Wire concrete adapters for desired-state source, secret resolver, status sink, registry auth, etc.
- Core: solid, explicit. Edges: more malleable.

## Public Boundary

This repo is public.

- Keep source, tests, CI, and local dev tasks here.
- Keep secrets, private identifiers, tenant data, live credentials, cloud project IDs out.
- Keep private deploy infra, publishing, maintainer live e2e, prod apply/console/log/ssh, runtime-env secret sync, CDN cache busting out.
