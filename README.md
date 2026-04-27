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

The installer writes to `~/.local/bin` by default. If that directory is not already on your `PATH`, it prints the shell command to add it.

devopsellence is agent-first. The installer prints the agent skill command; to install the CLI and skill together:

```bash
curl -fsSL https://www.devopsellence.com/lfg.sh | bash -s -- --install-agent-skill
```

Initialize the workspace:

```bash
devopsellence init --mode solo
```

Commit the app before the first deploy. devopsellence uses the current git commit as the workload revision and image tag:

```bash
git init # if this is not already a git checkout
git add .
git commit -m "initial deploy"
```

Register an existing SSH-accessible VM, install the agent, and attach it to the current environment:

```bash
devopsellence node create prod-1 --host 203.0.113.10 --user root --ssh-key ~/.ssh/id_ed25519
devopsellence agent install prod-1
devopsellence node attach prod-1
devopsellence doctor
```

Or create a Hetzner-backed node from the provider:

```bash
devopsellence provider login hetzner --token "$HCLOUD_TOKEN"
devopsellence node create prod-1 --provider hetzner --install --attach
devopsellence doctor
```

For provider-created solo nodes, `devopsellence node create` can generate a workspace-scoped SSH keypair under `$XDG_STATE_HOME/devopsellence/solo/keys/` (default: `~/.local/state/devopsellence/solo/keys/`) and reuse it for later node creation from the same workspace.

Deploy over SSH:

```bash
devopsellence deploy
devopsellence status
```

`devopsellence status` includes `public_urls` when it can infer where the app should be reachable. For default solo HTTP ingress, try the node URL it prints.

Solo deploy scope comes from the nodes attached to the current workspace/environment. Use `devopsellence node attach <name>` and `devopsellence node detach <name>` to change which nodes receive the deploy.

Public ingress is Envoy in both modes. For solo HTTPS, point DNS at each web node, then configure hostnames. Pass `--service` when the target web service is not already obvious:

```bash
devopsellence ingress set --service web --host app.example.com --tls-email ops@example.com
devopsellence ingress check --wait 5m
devopsellence deploy
```

Store solo-mode deploy secrets locally:

```bash
printf '%s' "$RAILS_MASTER_KEY" | devopsellence secret set RAILS_MASTER_KEY --service web --stdin
devopsellence secret list
```

To clean up a solo experiment on an existing SSH node, first detach the node from the environment, then uninstall the agent and remove devopsellence-managed runtime resources before forgetting the node locally:

```bash
devopsellence node detach prod-1
devopsellence agent uninstall prod-1 --yes
devopsellence node remove prod-1 --yes
```

`agent uninstall --yes` stops and disables `devopsellence-agent`, removes devopsellence-managed containers, removes the `devopsellence-envoy` container and `devopsellence` Docker network, deletes agent state, and removes `/usr/local/bin/devopsellence-agent`. Use `--keep-workloads` only when you intentionally want to stop the agent without cleaning remote runtime resources.

Solo mode keeps app config workload-only. Solo nodes, local environment attachments, and the latest desired environment snapshots live in `$XDG_STATE_HOME/devopsellence/solo/state.json` (default: `~/.local/state/devopsellence/solo/state.json` when `XDG_STATE_HOME` is unset). Generated solo SSH keys stay local under `$XDG_STATE_HOME/devopsellence/solo/keys/`.

## Shared mode

When you want sign-in, teams, org/project/env context, hosted deploy APIs, or managed node workflows:

```bash
devopsellence init --mode shared
devopsellence provider login hetzner --token "$HCLOUD_TOKEN"
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
schema_version: 6
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
    ports:
      - name: http
        port: 3000
    healthcheck:
      path: /up
      port: 3000
tasks:
  release:
    service: web
    command:
      - bin/rails
      - db:migrate
ingress:
  hosts:
    - app.example.com
  rules:
    - match:
        host: app.example.com
        path_prefix: /
      target:
        service: web
        port: http
  tls:
    mode: auto
    email: ops@example.com
  redirect_http: true
environments:
  staging:
    ingress:
      hosts:
        - staging.example.com
      rules:
        - match:
            host: staging.example.com
            path_prefix: /
          target:
            service: web
            port: http
  production:
    services:
      web:
        env:
          RAILS_ENV: production
```

### Example: run cloudflared as a normal service

Cloudflare Tunnel can live in normal app config instead of as special agent-managed behavior:

```yaml
services:
  web:
    ports:
      - name: http
        port: 3000

  cloudflared:
    image: docker.io/cloudflare/cloudflared:latest
    command: ["cloudflared"]
    args: ["tunnel", "run"]
    secret_refs:
      - name: TUNNEL_TOKEN
        secret: CLOUDFLARE_TUNNEL_TOKEN
```

Then store the token as a project secret and deploy normally:

```bash
devopsellence secret set CLOUDFLARE_TUNNEL_TOKEN --service cloudflared
devopsellence deploy
```

## Need more than solo mode?

When you want browser auth, team workflows, hosted deploy APIs, managed nodes, or a control plane, switch the workspace to `shared` and choose one of these paths:

- Managed devopsellence: start from [www.devopsellence.com](https://www.devopsellence.com).
- Self-hosted control plane: use [`control-plane/`](control-plane/) from this repo.

The product layering is deliberate:

- solo mode first.
- shared mode when coordination matters.
- hosted or self-hosted depending on how much convenience you want.

When you outgrow solo, `devopsellence init --mode shared` switches to control-plane workflows. Same config, same agent, same deploy verbs.

The design rationale lives in [`docs/vision.md`](docs/vision.md). The explicit ingress-rules + generic-services schema change is documented in [`docs/specs/2026-04-24-explicit-ingress-rules-and-generic-services.md`](docs/specs/2026-04-24-explicit-ingress-rules-and-generic-services.md).

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

GitHub binary releases for the agent and CLI are published from a manual public GitHub Actions workflow. Trigger `devopsellence release`, choose a branch/tag/SHA, and provide the release version for stable releases; for prereleases, `version` can be left blank and will be auto-derived. The workflow always rebuilds and republishes both binaries for the same shared release tag; prereleases can be rerun to replace an existing prerelease tag.

## Contributing

Issues are welcome.

Unsolicited code contributions are not currently accepted.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the current policy.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
