# devopsellence

VMs are enough.

devopsellence is a toolkit for deploying and running containerized apps. No PaaS. No platform. No extra abstraction layer.

Pick a workspace mode.

- `solo` keeps the loop local: your app, your VM, SSH, Docker, and the `devopsellence` CLI.
- `shared` keeps the same app model but adds sign-in, org/project/env context, hosted APIs, and team workflows.

|  | Solo | Shared |
|---|---|---|
| Infrastructure | SSH + your VMs | Control plane + your VMs |
| Auth | SSH keys | Browser (GitHub / Google) |
| Secrets | Local `.env` file | Encrypted server-side |
| Images | Streamed over SSH | Pushed to registry |
| HTTPS | Built-in (Envoy + Let's Encrypt) | Built-in (Envoy + tunnel or Let's Encrypt) |
| Team workflows | Single operator | Orgs, projects, environments |
| Best for | Side projects, single-dev apps | Teams, production, multi-env |

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

Or create a Hetzner-backed node from the provider:

```bash
devopsellence node create prod-1 --provider hetzner
devopsellence node attach prod-1
```

Deploy over SSH:

```bash
devopsellence deploy
devopsellence status
```

Solo deploy scope comes from the nodes attached to the current workspace/environment. Use `devopsellence node attach <name>` and `devopsellence node detach <name>` to change which nodes receive the deploy.

Public ingress is Envoy in both modes. For solo HTTPS, point DNS at each web node, then configure hostnames. Pass `--service` when the target web service is not already obvious:

```bash
devopsellence ingress set --service web --host app.example.com --tls-email ops@example.com
devopsellence ingress check --wait 5m
devopsellence deploy
```

Store solo-mode deploy secrets locally:

```bash
printf '%s' "$RAILS_MASTER_KEY" | devopsellence secret set RAILS_MASTER_KEY --stdin
devopsellence secret list
```

Solo mode keeps app config workload-only. Solo nodes, local environment attachments, and the latest desired environment snapshots live in `$XDG_STATE_HOME/devopsellence/solo/state.json` (default: `~/.local/state/devopsellence/solo/state.json` when `XDG_STATE_HOME` is unset).

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
schema_version: 5
app:
  type: rails
organization: solo
project: myapp
default_environment: production
build:
  context: .
  dockerfile: Dockerfile
  platforms:
    - linux/amd64
services:
  web:
    kind: web
    roles: [web]
    ports:
      - name: http
        port: 3000
    healthcheck:
      path: /up
      port: 3000
tasks:
  release:
    service: web
    command: bin/rails db:migrate
ingress:
  service: web
  hosts:
    - app.example.com
  tls:
    mode: auto
    email: ops@example.com
  redirect_http: true
```

## Need more than solo mode?

When you want browser auth, team workflows, hosted deploy APIs, managed nodes, or a control plane, switch the workspace to `shared` and choose one of these paths:

- Managed devopsellence: start from [www.devopsellence.com](https://www.devopsellence.com).
- Self-hosted control plane: use [`control-plane/`](control-plane/) from this repo.

The product layering is deliberate:

- solo mode first.
- shared mode when coordination matters.
- hosted or self-hosted depending on how much convenience you want.

When you outgrow solo, `devopsellence mode use shared` switches to control-plane workflows. Same config, same agent, same deploy verbs.

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

GitHub binary releases for the agent and CLI are published from a manual public GitHub Actions workflow. Trigger `devopsellence release`, choose a branch/tag/SHA, and provide the release version. The workflow always rebuilds and republishes both binaries for the same shared release tag; prereleases can be rerun to replace an existing prerelease tag.

## Contributing

Issues are welcome.

Unsolicited code contributions are not currently accepted.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the current policy.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
