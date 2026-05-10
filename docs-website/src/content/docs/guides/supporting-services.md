---
title: Supporting services
description: Run Redis, Memcached, and other companion containers beside an app.
---

Use another `services` entry when a dependency should run on the same VM as
your app. Set `image` for services that come from an upstream or separately
built container image. If `image` is omitted, the service uses the app image
built from `build.context` and `build.dockerfile`.

Supporting services are still ordinary workload services. They are reconciled
by the node agent, show up in status, and work with `logs`, `exec`, secrets,
and volumes. They do not get public ingress unless an ingress rule targets
them.

## Redis

```yaml
services:
  web:
    env:
      REDIS_URL: redis://redis:6379/0
    ports:
      - name: http
        port: 3000
    healthcheck:
      path: /up
      port: 3000

  redis:
    image: redis:7-alpine
    args:
      - redis-server
      - --appendonly
      - "yes"
    volumes:
      - source: redis_data
        target: /data
```

`redis` has no `ports`, `healthcheck`, or ingress rule because it is private to
the app environment. Services in the same environment can reach it by service
name on the environment Docker network.

## Memcached

```yaml
services:
  web:
    env:
      MEMCACHE_SERVERS: memcached:11211
    ports:
      - name: http
        port: 3000
    healthcheck:
      path: /up
      port: 3000

  memcached:
    image: memcached:1.6-alpine
    args:
      - memcached
      - -m
      - "128"
```

Memcached is usually disposable, so this example does not mount a volume.

## Scheduling

Services with a port named `http`, a healthcheck, or the exact service name
`web` are scheduled as `web`. Other services are scheduled as `worker`.

Solo nodes created with the default labels only run `web` services, so add
`worker` to any node that should host Redis, Memcached, background workers,
backup services, or other private runtime units:

```bash
devopsellence node label set prod-1 --labels web,worker
devopsellence deploy
```

In shared mode, use the same config shape. The control plane and node agent use
the same desired-state model; only image publication and secret storage differ.

## Release tasks

Treat companion services as runtime dependencies that may need their own
readiness checks before a release task uses them. A release task can run after
devopsellence has published desired state, but a container may exist before the
service inside it accepts connections.

Release tasks that need a database, cache, or queue should include retry or wait
logic before running migrations or setup commands.

Current `devopsellence.yml` supporting services are scheduled as workload
services. They do not yet expose first-class dependency or readiness settings,
so do not rely on this as a complete sidecar dependency system.

## Operational notes

Use named volumes for state you expect to keep across deploys. For Redis, that
means mounting `/data` when persistence is enabled. For services that are only
caches, prefer disposable configuration and keep the durable system of record
outside the cache.

Use service-scoped secrets when the supporting service needs credentials:

```yaml
services:
  web:
    env:
      SEARCH_URL: http://search:9200

  search:
    image: registry.example.com/search:<version>
    secret_refs:
      - name: SEARCH_PASSWORD
        secret: SEARCH_PASSWORD
```

```bash
devopsellence secret set SEARCH_PASSWORD --service search --stdin
```
