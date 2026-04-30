# devopsellence

Built for AI operators, transparent for humans.

devopsellence is an AI-operator-first deployment toolkit for containerized apps
on familiar VMs. It keeps the runtime boring: Docker, SSH, Envoy, files, JSON,
logs, and a node agent that reconciles desired state.

No PaaS. No Kubernetes-lite. No hidden scheduler pretending machines do not
exist.

## AI-operator-first

The main operator is an AI coding or operations assistant acting for a human.
devopsellence gives that AI operator narrow, auditable commands instead of
asking it to invent production shell choreography.

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
| Transport | SSH | node agent pulls published state |
| Secrets | local state or external refs | server-side team secret management |
| Runtime | same node agent | same node agent |

Solo and shared are management topologies, not separate deployment systems.

## AI operator quickstart

```bash
curl -fsSL https://www.devopsellence.com/lfg.sh | bash -s -- --install-agent-skill
cd my-app
codex e "Deploy this app with devopsellence solo. Inspect the repo, create or update devopsellence.yml, run devopsellence deploy --dry-run first, explain the plan, then apply it only if this prompt already gives enough approval to mutate the target VM."
```

Full docs: [docs.devopsellence.com](https://docs.devopsellence.com/).

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
- [AI operator direction](docs/agent-primary.md)
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
