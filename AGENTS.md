# AGENTS.md — devopsellence monorepo

Always write "devopsellence" in all lowercase.

## Repository layout

This is a monorepo with three independent components plus a root-owned test harness:

| Directory | Language | What it is |
|---|---|---|
| `agent/` | Go | Single-node reconciliation daemon. Reads desired state from GCS, pulls images from Artifact Registry, manages local containers via Docker. |
| `cli/` | Go | End-user CLI (`devopsellence` binary). Handles login, deploy, secrets, node management. Talks to the control plane API. |
| `control-plane/` | Ruby (Rails 8) | Web app + API. Manages tenants, deployments, nodes, GCP resources. Uses PostgreSQL. |
| `test/e2e/` | Ruby + shell | Hermetic monorepo integration lane spanning `agent/`, `cli/`, `control-plane/`, and in-tree `test/support/gcp-mock/`. |
| `test/support/gcp-mock/` | Go | Local emulator for the subset of GCP APIs used by the hermetic e2e lane. |

Each directory has its own `go.mod` / `Gemfile`, toolchain, tests, and CI. There is no shared build system — treat them as separate projects that happen to live together.

## Toolchain

All components use [mise](https://mise.jdx.dev) for tool versions and task running.

Root-level shortcuts (run from repo root):

```
mise run test:agent     # run agent tests
mise run test:cli       # run CLI tests
mise run test:cp        # run control plane tests
mise run e2e-shared     # run hermetic shared-mode e2e
mise run e2e-solo       # run hermetic solo-mode e2e
mise run test:all       # run all tests
mise run build:agent    # build agent
mise run build:cli      # build CLI
mise run fmt:agent      # format agent Go files
```

Per-component tasks (cd into the component first):

```
# agent/
mise run build          # build all Go packages
mise run test           # run tests
mise run fmt            # format Go files
mise run protoc         # generate protobuf Go code

# cli/
mise run build          # build CLI binary to bin/devopsellence
mise run test           # run tests

# control-plane/
mise run test           # run tests (starts Postgres automatically)
bin/dev                 # start dev server
```

Local override files and machine-specific templates should stay out of this public monorepo.

## Testing

- **agent/cli:** `mise run test` (Go tests, no external deps).
- **control-plane:** `mise run test` (needs Postgres via Docker; mise handles startup). Uses Minitest with mocha (stubs/mocks) and webmock (HTTP interception) — both are required in `test/test_helper.rb`. Run a single file with `mise run test -- test/path/file_test.rb`. Never write tests for static pages with no business logic.
- **e2e:** `mise run e2e-shared` or `mise run e2e-solo` from the repo root. They use in-tree `agent/`, `cli/`, and `test/support/gcp-mock/`.

## Code structure

**agent/** — `cmd/devopsellence/` (entrypoint), `internal/` (all packages: engine, reconcile, envoy, gcp, auth, cloudflared, etc.), `proto/` (protobuf definitions).

**cli/** — `cmd/devopsellence/` (entrypoint), `internal/` (packages: api, app, auth, docker, git, workflow, ui, etc.), `skills/` (OpenClaw skill).

**control-plane/** — standard Rails layout: `app/{models,controllers,services,views,jobs}`, `config/`, `db/` (migrations), `test/`, `script/` (local operational scripts only).

**test/e2e/** — root-owned hermetic integration lane: runner Dockerfile, runner wrapper, and the end-to-end smoke script.

**test/support/gcp-mock/** — in-tree GCP emulator used by the hermetic e2e lane.

## Conventions

- Go code uses `gofmt`. No linter config beyond that.
- Rails code uses `.rubocop.yml` for style.
- CI workflows live in `.github/workflows/` (root), with path filters per component.
- Public GitHub Release workflows for agent/CLI live here; private publishing and deploy automation should live outside this repository.

## Public Repo Boundary

- Keep this repo public-safe: source, tests, CI, and local development tasks.
- Treat this repo as effectively public at all times; never add secrets, private identifiers, or operational data you would not publish on GitHub.
- Keep private deploy infrastructure in a separate private repository outside this tree.
- Keep private publishing and build/release/deploy automation outside this repository.
- Keep maintainer-only live e2e flows outside this repository.
- Keep prod deploy/apply, prod console/log/ssh, runtime-env secret sync, and CDN cache busting out of this public repo.
- Do not add live credentials, cloud project IDs, or tenant-specific operational data to tracked files here.
