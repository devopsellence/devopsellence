---
title: Environment overlays
description: Use one devopsellence.yml with production and staging runtime differences.
---

Environment overlays let one `devopsellence.yml` describe the common application shape once, then override the runtime fields that differ per environment.

Use overlays for deployment/runtime differences:

- service environment variables
- service secret references
- ports, commands, args, health checks, images, and volumes when an environment needs them
- release-task service, command, args, and env
- ingress hosts, route rules, TLS settings, and HTTP redirects

Do not use overlays for Docker build differences. `build.context`, `build.dockerfile`, and `build.platforms` are intentionally top-level only. The default contract is that staging and production deploy the same application artifact with different runtime configuration. If two environments need materially different builds, model that explicitly as a different image/service/build pipeline instead of hiding it inside an environment overlay.

## Production and staging example

Keep shared defaults at the top level, then put each environment's hostnames, runtime env, secret refs, and release-task env under `environments`.

```yaml title="devopsellence.yml"
schema_version: 1
organization: solo
project: rails-app
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
    env:
      RAILS_LOG_TO_STDOUT: "true"
    secret_refs:
      - name: SECRET_KEY_BASE
        secret: SECRET_KEY_BASE

tasks:
  release:
    service: web
    command:
      - bin/rails
      - db:migrate

ingress:
  tls:
    mode: auto
    email: ops@example.com
  redirect_http: true

environments:
  production:
    services:
      web:
        env:
          RAILS_ENV: production
          BASE_URL: https://app.example.com
        secret_refs:
          - name: DATABASE_URL
            secret: DATABASE_URL
          - name: SECRET_KEY_BASE
            secret: SECRET_KEY_BASE
    tasks:
      release:
        env:
          RAILS_ENV: production
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

  staging:
    services:
      web:
        env:
          RAILS_ENV: staging
          BASE_URL: https://staging.example.com
        secret_refs:
          - name: DATABASE_URL
            secret: DATABASE_URL
          - name: SECRET_KEY_BASE
            secret: SECRET_KEY_BASE
    tasks:
      release:
        env:
          RAILS_ENV: staging
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
```

## Selecting an environment

`default_environment` is used when no environment is selected. To target another environment, pass `--env` on commands that support it:

```bash
devopsellence config resolve --env staging
devopsellence deploy --env staging
devopsellence status --env staging
```

You can also set the environment for a command with `DEVOPSELLENCE_ENVIRONMENT`:

```bash
DEVOPSELLENCE_ENVIRONMENT=staging devopsellence deploy
```

For secrets, set values in the same environment you deploy:

```bash
printf '%s' "$STAGING_DATABASE_URL" | devopsellence secret set DATABASE_URL --service web --env staging --stdin
printf '%s' "$PRODUCTION_DATABASE_URL" | devopsellence secret set DATABASE_URL --service web --env production --stdin
```

## Merge behavior

Environment overlays are merged onto the top-level config before deploy:

- service overlays merge into the matching top-level service
- service `env` maps are merged by key
- `secret_refs`, `ports`, `command`, `args`, and `volumes` replace the corresponding service field when present
- release-task overlays merge into `tasks.release`
- ingress overlays merge into top-level ingress
- ingress `hosts` and `rules` replace the top-level host/rule set when present

Keep top-level config for values that are the same everywhere. Put only environment-specific differences in the overlay.

## Ingress rules, not `ingress.service`

Ingress uses `hosts` plus `rules` instead of a single `ingress.service` shortcut. Rules are the preferred shape because they support multi-host and path-based routing without assuming every app has exactly one public service.

A rule maps a host/path prefix to a service port:

```yaml
match:
  host: app.example.com
  path_prefix: /
target:
  service: web
  port: http
```

The `target.port` value is the named service port, not a raw host port.
