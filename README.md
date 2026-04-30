# devopsellence

Built for agents, transparent for humans.

devopsellence is an agent-primary deployment toolkit for containerized apps on
VMs you control. It keeps the runtime boring: Docker, SSH, Envoy, files, JSON,
logs, and a node agent that reconciles desired state.

No PaaS. No Kubernetes-lite. No hidden scheduler pretending machines do not
exist.

## Agent-primary

The main operator is an AI coding or operations agent acting for a human.
devopsellence gives that agent narrow, auditable commands instead of asking it
to invent production shell choreography.

- inspect, validate, plan, deploy, status, doctor, logs, rollback;
- structured JSON and deterministic exit codes as the contract;
- explicit dry-run, approval, apply, and rollback boundaries;
- desired state as the write boundary;
- ordinary tools remain valid when humans need to inspect or recover.

The node agent is deterministic. There is no LLM in the runtime reconciler.

## Modes

| | Solo | Shared |
|---|---|---|
| Best for | single operator, one app, direct VM ownership | teams, API tokens, org/project/env workflows |
| Control surface | local CLI and files | control plane |
| Transport | SSH | agent pulls published state |
| Secrets | local state or external refs | server-side team secret management |
| Runtime | same node agent | same node agent |

Solo and shared are management topologies, not separate deployment systems.

## Solo quickstart

```bash
curl -fsSL https://www.devopsellence.com/lfg.sh | bash
devopsellence init --mode solo
devopsellence node create prod-1 --host 203.0.113.10 --user root --ssh-key ~/.ssh/id_ed25519
devopsellence agent install prod-1
devopsellence node attach prod-1
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
```

To install the CLI and Codex skill together:

```bash
curl -fsSL https://www.devopsellence.com/lfg.sh | bash -s -- --install-agent-skill
```

## Example config

`devopsellence` reads `devopsellence.yml` from the app root:

```yaml
schema_version: 1
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
```

## Learn more

- [docs website component](docs-website/)
- [agent-primary direction](docs/agent-primary.md)
- [vision](docs/vision.md)
- [north star](docs/north-star.md)

## Monorepo layout

| Directory | Description |
|---|---|
| [`agent/`](agent/) | single-node reconciliation daemon |
| [`cli/`](cli/) | end-user CLI for solo and shared workflows |
| [`control-plane/`](control-plane/) | Rails API and web app |
| [`deployment-core/`](deployment-core/) | shared Go deployment model and desired-state generation |
| [`docs-website/`](docs-website/) | public static documentation site |
| [`test/e2e/`](test/e2e/) | hermetic monorepo integration lane |
| [`test/support/gcp-mock/`](test/support/gcp-mock/) | local GCP emulator used by hermetic e2e |

## Developing

Use `mise`.

```bash
mise install
mise run test:agent
mise run test:cli
mise run test:core
mise run test:cp
mise run e2e-shared
mise run e2e-solo
```

Per component:

```bash
cd agent && mise run test
cd cli && mise run test
cd control-plane && mise run test
cd deployment-core && mise run test
cd docs-website && mise run build
```

## Contributing

Issues are welcome.

Unsolicited code contributions are not currently accepted. See
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).
