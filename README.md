# devopsellence

VMs are enough.

devopsellence is a toolkit for deploying and running containerized apps. No PaaS. No platform. No extra abstraction layer.

Pick a workspace mode.

- `solo` keeps the loop local: your app, your VM, SSH, Docker, and the `devopsellence` CLI.
- `shared` keeps the same app model but adds sign-in, org/project/env context, hosted APIs, and team workflows.

## Start Here: solo mode

Install the CLI:

```bash
curl -fsSL https://www.devopsellence.com/lfg.sh | bash
```

Check local tooling:

```bash
devopsellence doctor
```

Choose the workspace mode once:

```bash
devopsellence mode use solo
```

Prepare the app, connect a node, and install the agent:

```bash
devopsellence provider login hetzner
devopsellence setup
```

Or create a Hetzner-backed node directly:

```bash
devopsellence node create prod-1 --provider hetzner
```

Deploy over SSH:

```bash
devopsellence deploy
devopsellence status
```

Today, solo mode exposes the app through Envoy on `http://<server>:8000`.
Shared-style tunnel mode and automatic SSL for solo mode are planned, but not available yet.

Store solo-mode deploy secrets locally:

```bash
printf '%s' "$RAILS_MASTER_KEY" | devopsellence secret set RAILS_MASTER_KEY --stdin
devopsellence secret list
```

Solo mode keeps the mental model simple: build locally, transfer the image, write desired state, let the agent reconcile.

## Shared mode

When you want sign-in, teams, org/project/env context, hosted deploy APIs, or managed node workflows:

```bash
devopsellence mode use shared
devopsellence setup
devopsellence provider login hetzner
devopsellence node create prod-1 --provider hetzner
devopsellence deploy
devopsellence status
devopsellence open
```

The root verbs stay the same. The selected workspace mode decides how they behave.
In shared mode, `node create` provisions the server and runs the registration install command.

### Example config

`devopsellence` reads `devopsellence.yml` from the app root:

```yaml
schema_version: 3
app:
  type: rails
organization: direct
project: myapp
default_environment: production
build:
  context: .
  dockerfile: Dockerfile
  platforms:
    - linux/amd64
web:
  port: 3000
  healthcheck:
    path: /up
    port: 3000
release_command: bin/rails db:migrate
direct:
  nodes:
    prod-1:
      host: 203.0.113.10
      user: root
      ssh_key: ~/.ssh/id_ed25519
      labels:
        - web
```

## Need more than solo mode?

When you want browser auth, team workflows, hosted deploy APIs, managed nodes, or a control plane, switch the workspace to `shared` and choose one of these paths:

- Managed devopsellence: start from [www.devopsellence.com](https://www.devopsellence.com).
- Self-hosted control plane: use [`control-plane/`](control-plane/) from this repo.

The product layering is deliberate:

- solo mode first.
- shared mode when coordination matters.
- hosted or self-hosted depending on how much convenience you want.

The design rationale lives in [`docs/vision.md`](docs/vision.md).

## Monorepo layout

This repo contains three components plus a root-owned test harness:

| Directory | Description |
|---|---|
| [`agent/`](agent/) | Single-node reconciliation daemon |
| [`cli/`](cli/) | End-user CLI for solo and shared workflows |
| [`control-plane/`](control-plane/) | Rails API and web app |
| [`test/e2e/`](test/e2e/) | Hermetic monorepo integration lane |
| [`test/support/gcp-mock/`](test/support/gcp-mock/) | Local GCP emulator used by hermetic e2e |

## Developing

This monorepo uses [mise](https://mise.jdx.dev) for toolchains and tasks.

From the repo root:

```bash
mise install
mise run test:agent
mise run test:cli
mise run test:cp
mise run e2e-shared
mise run e2e-solo
```

Per component:

```bash
cd agent && mise run test
cd cli && mise run test
cd control-plane && mise run test
```

For local control-plane development:

```bash
cd control-plane
bin/dev
```

GitHub binary releases for the agent and CLI are published from public GitHub Actions workflows on `agent-v*` and `cli-v*` tags.

## Contributing

Issues are welcome.

Unsolicited code contributions are not currently accepted.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the current policy.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
