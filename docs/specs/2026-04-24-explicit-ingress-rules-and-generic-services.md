# Explicit ingress rules with generic services

## Summary

Replace service kinds (`web`, `worker`, `accessory`) and singleton `ingress.service` routing with a simpler model:

- all workloads are declared under `services`
- service ports are explicit
- ingress stays root-level
- ingress uses explicit `rules[]`
- every ingress rule explicitly targets a service and port
- path-based and host-based fan-out are first-class

This keeps `services` generic and makes exposure/routing an ingress concern instead of a service kind concern.

---

## Problem

Today the repo config still couples routing to a special service classification:

- `cli/internal/config/config.go` defines service kinds `web`, `worker`, and `accessory`
- validation requires at least one `kind: web` service
- `ingress` points to a single `ingress.service`
- that target service must be `kind: web`

That model is too narrow for where devopsellence is heading:

1. The kinds do not carry enough distinct schema or lifecycle value to justify being first-class.
2. Routing is artificially attached to one service instead of being an app-level concern.
3. Path-based fan-out (`/api` -> one service, `/` -> another) does not fit naturally.
4. The config shape is behind the desired-state model: the node agent desired-state proto already has `ingress.routes[].match` and `ingress.routes[].target { environment, service, port }`.

The result is a split model where repo config is more opinionated and less expressive than the runtime shape.

---

## Goals

1. Remove the need for service kinds in repo config.
2. Keep ingress at the root level.
3. Make ingress routing explicit through `ingress.rules[]`.
4. Require explicit `target.service` and `target.port` per ingress rule.
5. Keep service ports explicit; do not rely on implicit ingress port inference.
6. Support multiple hosts and path-based fan-out cleanly.
7. Keep solo/shared semantics aligned by mapping the repo config directly onto the existing desired-state routing model.

---

## Non-goals

1. Do not add higher-level roles as a replacement for kinds.
2. Do not add implicit routing heuristics such as "the only service with an http port becomes the ingress target".
3. Do not introduce advanced routing features yet beyond host + path prefix matching.
4. Do not make ingress service-local.
5. Do not preserve `kind` long-term as a semantic requirement in core behavior.

---

## Proposed config shape

### Before

```yaml
schema_version: 5
organization: acme
project: demo
default_environment: production

build:
  context: .
  dockerfile: Dockerfile

services:
  web:
    kind: web
    command: ["./bin/web"]
    ports:
      - name: http
        port: 3000
    healthcheck:
      path: /up
      port: 3000

  worker:
    kind: worker
    command: ["./bin/worker"]

ingress:
  service: web
  hosts:
    - app.example.com
  tls:
    mode: auto
    email: ops@example.com
  redirect_http: true
```

### After

```yaml
schema_version: 6
organization: acme
project: demo
default_environment: production

build:
  context: .
  dockerfile: Dockerfile

services:
  app:
    command: ["./bin/web"]
    ports:
      - name: http
        port: 3000
    healthcheck:
      path: /up
      port: 3000

  api:
    command: ["./bin/api"]
    ports:
      - name: http
        port: 4000
    healthcheck:
      path: /up
      port: 4000

  worker:
    command: ["./bin/worker"]

ingress:
  hosts:
    - app.example.com
  tls:
    mode: auto
    email: ops@example.com
  redirect_http: true
  rules:
    - match:
        host: app.example.com
        path_prefix: /api
      target:
        service: api
        port: http

    - match:
        host: app.example.com
        path_prefix: /
      target:
        service: app
        port: http
```

### Single-service app

Even the simple case stays explicit:

```yaml
services:
  app:
    command: ["./bin/web"]
    ports:
      - name: http
        port: 3000

ingress:
  hosts:
    - app.example.com
  rules:
    - match:
        host: app.example.com
        path_prefix: /
      target:
        service: app
        port: http
```

---

## Schema changes

### `ServiceConfig`

Remove:

- `kind`

Keep services as generic workload definitions with fields like:

- `image`
- `command`
- `args`
- `env`
- `secret_refs`
- `ports`
- `healthcheck`
- `volumes`

### `IngressConfig`

Replace:

```yaml
ingress:
  service: web
  hosts: [...]
```

with:

```yaml
ingress:
  hosts: [...]
  rules:
    - match:
        host: app.example.com
        path_prefix: /
      target:
        service: app
        port: http
```

Suggested config structs:

```go
type IngressConfig struct {
    Hosts        []string            `yaml:"hosts,omitempty" json:"hosts,omitempty"`
    Rules        []IngressRuleConfig `yaml:"rules,omitempty" json:"rules,omitempty"`
    TLS          IngressTLSConfig    `yaml:"tls,omitempty" json:"tls,omitempty"`
    RedirectHTTP bool                `yaml:"redirect_http,omitempty" json:"redirect_http,omitempty"`
}

type IngressRuleConfig struct {
    Match  IngressMatchConfig  `yaml:"match" json:"match"`
    Target IngressTargetConfig `yaml:"target" json:"target"`
}

type IngressMatchConfig struct {
    Host       string `yaml:"host" json:"host"`
    PathPrefix string `yaml:"path_prefix,omitempty" json:"path_prefix,omitempty"`
}

type IngressTargetConfig struct {
    Service string `yaml:"service" json:"service"`
    Port    string `yaml:"port" json:"port"`
}
```

Notes:

- Use `host` in repo config for the rule match to keep the field singular at rule level.
- The desired-state protobuf currently uses `hostname`; config translation can map `host -> hostname`.
- `target.environment` is not needed in repo config because the config is resolved for one target environment before desired-state generation.

---

## Validation rules

### Services

1. `services` must be non-empty.
2. Service names must remain unique.
3. If a service declares `ports`, port names must be unique within that service.
4. No service kind validation exists.
5. Do not require at least one `web` service.

### Ingress

1. `ingress.hosts` must be non-empty when `ingress` is present.
2. `ingress.rules` must be non-empty when `ingress` is present.
3. Every `rule.match.host` is required.
4. Every `rule.match.host` must exist in `ingress.hosts`.
5. Every `rule.match.path_prefix` must start with `/`; if omitted, normalize to `/`.
6. Every `rule.target.service` is required and must reference an existing service.
7. Every `rule.target.port` is required and must reference an existing named port on the target service.
8. Reject duplicate routes for the same `(host, path_prefix)`.
9. Do not require the target service to be a special kind.
10. Do not require the target port to be named `http`; explicit named ports should be routable as long as Envoy/runtime handling supports them.

### Healthchecks

Do not keep `healthcheck` coupled to a removed `web` kind. A service with an HTTP healthcheck should be valid regardless of whether it has ingress.

---

## Desired-state mapping

This change should move the repo config closer to the existing desired-state model rather than inventing a second routing abstraction.

Current desired-state already supports:

- `ingress.hosts`
- `ingress.routes[]`
- `ingress.routes[].match.hostname`
- `ingress.routes[].match.path_prefix`
- `ingress.routes[].target.environment`
- `ingress.routes[].target.service`
- `ingress.routes[].target.port`

Config translation should therefore be straightforward:

- copy `ingress.hosts`
- map each config rule to a desired-state route
- set `target.environment` from the selected deployment environment
- copy explicit `target.service` and `target.port`

### Node placement constraint

When ingress rules target multiple services, the published ingress desired state must only be attached to nodes that host every targeted service for that environment. Do not advertise or provision public ingress on nodes that host only a subset of the routed services.

---

## Required implementation changes

### CLI config model and validation

Update `cli/internal/config/config.go` to:

- remove `ServiceConfig.Kind`
- remove `ServiceKindWeb`, `ServiceKindWorker`, `ServiceKindAccessory`
- remove validation that requires a web service
- replace `IngressConfig.Service` with `IngressConfig.Rules`
- validate explicit rule targets and explicit target ports
- stop defaulting service behavior based on kind

### Solo desired-state generation

Update `cli/internal/solo/desiredstate.go` to:

- stop generating one route per host from `ingress.service`
- serialize the configured `ingress.rules[]` directly
- populate desired-state target environment from the selected environment
- keep target ports explicit instead of forcing `http`

### Node agent desired-state validation

Update `agent/internal/desiredstate/validate.go` to:

- stop requiring ingress targets to be kind `web`
- stop requiring target port to be `http`
- validate only that the referenced target service and port exist

### Runtime / routing assumptions

Audit code paths that still assume:

- ingress implies `kind web`
- the routed port must be named `http`
- there is a single ingress service

The desired-state proto and merge logic already model per-route service+port targeting, so the remaining cleanup should mostly be validation and config translation.

---

## Migration

This is a schema change and should be a clean break under `schema_version: 6`.

### Automatic mapping from v5 to v6

A straightforward migration exists for current configs:

```yaml
ingress:
  service: web
  hosts:
    - app.example.com
```

becomes:

```yaml
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
```

For services:

- drop `kind: web`
- drop `kind: worker`
- drop `kind: accessory`
- preserve the rest unchanged

### Compatibility stance

Prefer a clean schema-versioned break over compatibility shims that keep the old taxonomy alive in the core model.

---

## Open questions

1. Should `ingress.rules[].match.path_prefix` default to `/` when omitted, or should it be required for maximum explicitness?
   - Recommendation: allow omission and normalize to `/`.

2. Should `ingress.hosts` remain required if every rule already has a host?
   - Recommendation: yes. It keeps TLS/certificate scope and route coverage obvious, and it matches current desired-state validation.

3. Should `ports` be required on every ingress-targetable service?
   - Recommendation: yes indirectly, because rule validation requires the target port to exist.

---

## Acceptance criteria

1. Repo config no longer supports or requires service kinds.
2. Repo config ingress uses explicit `rules[]` with `target.service` and `target.port`.
3. Path-based routing to different services is supported in config and desired state.
4. Ingress target validation no longer depends on `kind: web`.
5. Ingress target validation no longer hardcodes `port: http`.
6. README examples and setup output reflect the new schema.
7. Solo and shared flows continue to produce the same desired-state ingress model.

---

## Suggested rollout

1. Land schema and validation changes behind schema version 6.
2. Update desired-state generation and node agent validation.
3. Update setup/init templates and README examples.
4. Add migration/docs notes for old `ingress.service` + `kind` configs.

---

## Why this matches devopsellence direction

This keeps the product centered on explicit runtime intent instead of category labels:

- generic services
- explicit ports
- explicit routing
- one ingress model
- fewer magic distinctions between "web" and everything else

That is more composable, easier to reason about, and better aligned with the existing desired-state routing shape.
