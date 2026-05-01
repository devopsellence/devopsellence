---
title: Deploy Basecamp Fizzy with solo
description: Use devopsellence solo instead of Kamal to deploy a real Rails app, worker, and staging environment on a VM.
---

[Fizzy](https://github.com/basecamp/fizzy) is Basecamp's open-source Rails kanban app. It is a useful
example because it is not a toy: it ships a production Dockerfile, uses SQLite-backed Rails subsystems,
has recurring Solid Queue jobs, needs persistent storage, has secrets, sends mail, and exposes the standard
Rails `/up` health check.

This guide shows the same deployment concerns Fizzy's `config/deploy.yml` describes for Kamal, but with
`devopsellence solo`: local config and secrets, SSH to your VM, direct image transfer, desired-state
publication, and a deterministic node agent reconcile loop.

It then expands the single-service deployment in two ways:

1. run Solid Queue as a separate `worker` service instead of inside Puma
2. add a `staging` environment layered over the production-shaped base config

## What changes from Kamal

Fizzy's Kamal config has these deployment concerns:

- one `web` container built from the app's `Dockerfile`
- port `80` inside the container, served by Thruster
- `/rails/storage` persisted for SQLite, Solid Queue, Solid Cache, Solid Cable, and Active Storage
- automatic TLS for `fizzy.example.com`
- clear environment values such as `BASE_URL`, `MAILER_FROM_ADDRESS`, `SMTP_ADDRESS`, and
  `SOLID_QUEUE_IN_PUMA=true`
- secrets such as `SECRET_KEY_BASE`, VAPID keys, and SMTP credentials

In devopsellence those concerns live in `devopsellence.yml` plus local solo secrets. Node inventory stays
outside the app config.

## Prerequisites

- a VM reachable over SSH with Docker installed, or a provider-backed solo node created by devopsellence
- DNS names such as `fizzy.example.com` and `staging.fizzy.example.com` pointing at the VM
- the devopsellence CLI installed locally
- a local clone of Fizzy

```bash
git clone https://github.com/basecamp/fizzy.git
cd fizzy
```

If this is the first devopsellence deployment from the repo, initialize solo mode:

```bash
devopsellence init --mode solo
```

## Start with the Kamal-equivalent single-service shape

For the closest Kamal translation, keep Solid Queue inside Puma. Replace `fizzy.example.com`,
`ops@example.com`, and mail settings with your values.

```yaml
schema_version: 1
organization: solo
project: fizzy
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
        port: 80
    healthcheck:
      path: /up
      port: 80
    volumes:
      - source: fizzy_storage
        target: /rails/storage
    env:
      RAILS_ENV: production
      BASE_URL: https://fizzy.example.com
      MAILER_FROM_ADDRESS: support@example.com
      SMTP_ADDRESS: mail.example.com
      MULTI_TENANT: "false"
      SOLID_QUEUE_IN_PUMA: "true"
    secret_refs:
      - name: SECRET_KEY_BASE
        secret: SECRET_KEY_BASE
      - name: VAPID_PUBLIC_KEY
        secret: VAPID_PUBLIC_KEY
      - name: VAPID_PRIVATE_KEY
        secret: VAPID_PRIVATE_KEY
      - name: SMTP_USERNAME
        secret: SMTP_USERNAME
      - name: SMTP_PASSWORD
        secret: SMTP_PASSWORD

tasks:
  release:
    service: web
    command:
      - ./bin/rails
      - db:prepare

ingress:
  hosts:
    - fizzy.example.com
  rules:
    - match:
        host: fizzy.example.com
        path_prefix: /
      target:
        service: web
        port: http
  tls:
    mode: auto
    email: ops@example.com
  redirect_http: true
```

Why this shape works:

- Fizzy's Dockerfile exposes port `80`, so the service port and health check target `80`.
- Fizzy's container entrypoint runs `db:prepare` before the Rails server. The release task runs it before
  rollout as well, so the new release is prepared before traffic moves.
- The named volume maps to `/rails/storage`, matching Fizzy's Kamal volume and Rails SQLite storage paths.
- `SOLID_QUEUE_IN_PUMA=true` keeps the OSS single-node deployment to one service container.

## Split Solid Queue into a worker service

Fizzy also ships `bin/jobs`, which runs `SolidQueue::Cli`. To run jobs separately, disable the Puma Solid
Queue plugin in `web`, then add a `worker` service that uses the same image, storage volume, env, and
secrets.

```yaml
schema_version: 1
organization: solo
project: fizzy
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
        port: 80
    healthcheck:
      path: /up
      port: 80
    volumes:
      - source: fizzy_storage
        target: /rails/storage
    env: &app_env
      RAILS_ENV: production
      BASE_URL: https://fizzy.example.com
      MAILER_FROM_ADDRESS: support@example.com
      SMTP_ADDRESS: mail.example.com
      MULTI_TENANT: "false"
      SOLID_QUEUE_IN_PUMA: "false"
    secret_refs: &app_secrets
      - name: SECRET_KEY_BASE
        secret: SECRET_KEY_BASE
      - name: VAPID_PUBLIC_KEY
        secret: VAPID_PUBLIC_KEY
      - name: VAPID_PRIVATE_KEY
        secret: VAPID_PRIVATE_KEY
      - name: SMTP_USERNAME
        secret: SMTP_USERNAME
      - name: SMTP_PASSWORD
        secret: SMTP_PASSWORD

  worker:
    command:
      - ./bin/jobs
    volumes:
      - source: fizzy_storage
        target: /rails/storage
    env: *app_env
    secret_refs: *app_secrets

tasks:
  release:
    service: web
    command:
      - ./bin/rails
      - db:prepare

ingress:
  hosts:
    - fizzy.example.com
  rules:
    - match:
        host: fizzy.example.com
        path_prefix: /
      target:
        service: web
        port: http
  tls:
    mode: auto
    email: ops@example.com
  redirect_http: true
```

The worker has no `ports`, `healthcheck`, or ingress rule because it is a background process, not an HTTP
endpoint. It shares `/rails/storage` with `web` because Fizzy's queue database lives under the same storage
path in the default SQLite deployment.

## Add staging with layered config

Use environment overlays when production and staging share the same app shape but differ in hostnames,
clear env values, volumes, and secret values.

This example keeps common settings at the top level:

- both environments build the same Dockerfile
- both run `web` plus `worker`
- both use `RAILS_ENV=production`, because Rails production mode is still the right runtime mode for a
  deployed staging instance
- both use the same secret names in `secret_refs`

The `production` and `staging` overlays then layer environment-specific values on top:

- `services.*.env` maps are merged, so `BASE_URL` and SMTP settings can differ per environment while
  inherited keys such as `RAILS_ENV` and `SOLID_QUEUE_IN_PUMA` stay common
- `services.*.volumes` are replaced, so staging can use a separate SQLite/storage volume
- `ingress.hosts` and `ingress.rules` are replaced, so each environment gets its own host routing
- `secret_refs` remain inherited; set separate secret values in solo state with `--env staging`

```yaml
schema_version: 1
organization: solo
project: fizzy
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
        port: 80
    healthcheck:
      path: /up
      port: 80
    volumes: &production_storage
      - source: fizzy_production_storage
        target: /rails/storage
    env: &base_env
      RAILS_ENV: production
      MULTI_TENANT: "false"
      SOLID_QUEUE_IN_PUMA: "false"
    secret_refs: &base_secrets
      - name: SECRET_KEY_BASE
        secret: SECRET_KEY_BASE
      - name: VAPID_PUBLIC_KEY
        secret: VAPID_PUBLIC_KEY
      - name: VAPID_PRIVATE_KEY
        secret: VAPID_PRIVATE_KEY
      - name: SMTP_USERNAME
        secret: SMTP_USERNAME
      - name: SMTP_PASSWORD
        secret: SMTP_PASSWORD

  worker:
    command:
      - ./bin/jobs
    volumes: *production_storage
    env: *base_env
    secret_refs: *base_secrets

tasks:
  release:
    service: web
    command:
      - ./bin/rails
      - db:prepare

ingress:
  hosts:
    - fizzy.example.com
  rules:
    - match:
        host: fizzy.example.com
        path_prefix: /
      target:
        service: web
        port: http
  tls:
    mode: auto
    email: ops@example.com
  redirect_http: true

environments:
  production:
    services:
      web:
        env:
          BASE_URL: https://fizzy.example.com
          MAILER_FROM_ADDRESS: support@example.com
          SMTP_ADDRESS: mail.example.com
          WEB_CONCURRENCY: "2"
      worker:
        env:
          BASE_URL: https://fizzy.example.com
          MAILER_FROM_ADDRESS: support@example.com
          SMTP_ADDRESS: mail.example.com
    ingress:
      hosts:
        - fizzy.example.com
      rules:
        - match:
            host: fizzy.example.com
            path_prefix: /
          target:
            service: web
            port: http
      tls:
        mode: auto
        email: ops@example.com
      redirect_http: true

  staging:
    services:
      web:
        volumes: &staging_storage
          - source: fizzy_staging_storage
            target: /rails/storage
        env:
          BASE_URL: https://staging.fizzy.example.com
          MAILER_FROM_ADDRESS: staging-support@example.com
          SMTP_ADDRESS: sandbox-smtp.example.com
          WEB_CONCURRENCY: "1"
      worker:
        volumes: *staging_storage
        env:
          BASE_URL: https://staging.fizzy.example.com
          MAILER_FROM_ADDRESS: staging-support@example.com
          SMTP_ADDRESS: sandbox-smtp.example.com
    ingress:
      hosts:
        - staging.fizzy.example.com
      rules:
        - match:
            host: staging.fizzy.example.com
            path_prefix: /
          target:
            service: web
            port: http
      tls:
        mode: auto
        email: ops@example.com
      redirect_http: true
```

This layout demonstrates the intended layering model: the top-level config describes the app's common
runtime shape; environment overlays describe what changes for a specific environment.

## Set secrets

Generate app secrets locally, then store them in solo state. Prefer `--stdin` so values do not land in
shell history.

```bash
bin/rails secret | devopsellence secret set SECRET_KEY_BASE --service web --stdin
```

Generate VAPID keys using the app bundle, then set both values:

```bash
bin/rails runner 'key = WebPush.generate_key; puts key.public_key; puts key.private_key'
printf '%s' '<public-key>' | devopsellence secret set VAPID_PUBLIC_KEY --service web --stdin
printf '%s' '<private-key>' | devopsellence secret set VAPID_PRIVATE_KEY --service web --stdin
```

Set SMTP credentials if this instance will send mail:

```bash
printf '%s' '<smtp-username>' | devopsellence secret set SMTP_USERNAME --service web --stdin
printf '%s' '<smtp-password>' | devopsellence secret set SMTP_PASSWORD --service web --stdin
```

For the split-worker config, the worker uses the same secret names. Set them for `worker` too, or use the
same 1Password references for both services:

```bash
SECRET_KEY_BASE=$(bin/rails secret)
for service in web worker; do
  printf '%s' "$SECRET_KEY_BASE" | devopsellence secret set SECRET_KEY_BASE --service "$service" --stdin
  printf '%s' '<public-key>' | devopsellence secret set VAPID_PUBLIC_KEY --service "$service" --stdin
  printf '%s' '<private-key>' | devopsellence secret set VAPID_PRIVATE_KEY --service "$service" --stdin
  printf '%s' '<smtp-username>' | devopsellence secret set SMTP_USERNAME --service "$service" --stdin
  printf '%s' '<smtp-password>' | devopsellence secret set SMTP_PASSWORD --service "$service" --stdin
done
```

For staging, set values in the staging environment. Use different secrets if staging should be isolated
from production:

```bash
STAGING_SECRET_KEY_BASE=$(bin/rails secret)
for service in web worker; do
  printf '%s' "$STAGING_SECRET_KEY_BASE" | devopsellence secret set SECRET_KEY_BASE --service "$service" --env staging --stdin
  printf '%s' '<staging-vapid-public-key>' | devopsellence secret set VAPID_PUBLIC_KEY --service "$service" --env staging --stdin
  printf '%s' '<staging-vapid-private-key>' | devopsellence secret set VAPID_PRIVATE_KEY --service "$service" --env staging --stdin
  printf '%s' '<staging-smtp-username>' | devopsellence secret set SMTP_USERNAME --service "$service" --env staging --stdin
  printf '%s' '<staging-smtp-password>' | devopsellence secret set SMTP_PASSWORD --service "$service" --env staging --stdin
done
```

You can also store solo secrets as 1Password references instead of plaintext local values:

```bash
devopsellence secret set SMTP_PASSWORD --service web --store 1password --op-ref op://deploy/fizzy/smtp-password
```

## Attach nodes

For an existing VM:

```bash
devopsellence node create prod-1 --host <server-ip-or-hostname> --user root --ssh-key ~/.ssh/id_ed25519
devopsellence agent install prod-1
devopsellence node attach prod-1
```

For a provider-created Hetzner node:

```bash
printf '%s' "$HCLOUD_TOKEN" | devopsellence provider login hetzner --stdin
devopsellence node create prod-1 --provider hetzner --install --attach
```

To co-host staging on the same VM, attach the node to staging too:

```bash
devopsellence node attach prod-1 --env staging
```

## Deploy production and staging

Check the production workspace before applying changes:

```bash
devopsellence doctor
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
```

Deploy staging by selecting the environment explicitly:

```bash
DEVOPSELLENCE_ENVIRONMENT=staging devopsellence doctor
devopsellence deploy --env staging --dry-run
devopsellence deploy --env staging
devopsellence status --env staging
```

Verify the real endpoints, not just the CLI output:

```bash
curl -fsS https://fizzy.example.com/up
curl -fsS https://staging.fizzy.example.com/up
curl -I http://fizzy.example.com/
curl -I http://staging.fizzy.example.com/
```

If TLS is still pending, run the explicit ingress readiness checks and then retry the HTTPS probes:

```bash
devopsellence ingress check --wait 2m
devopsellence ingress check --env staging --wait 2m
curl -fsS https://fizzy.example.com/up
curl -fsS https://staging.fizzy.example.com/up
```

## Operate it

Useful replacements for Fizzy's Kamal aliases:

```bash
# Rails console
devopsellence exec web -- ./bin/rails console

# Staging Rails console
devopsellence exec --env staging web -- ./bin/rails console

# Shell
devopsellence exec web -- bash

# Web logs
devopsellence logs web --node prod-1 --lines 200

# Worker logs
devopsellence logs worker --node prod-1 --lines 200

# Staging worker logs
devopsellence logs worker --env staging --node prod-1 --lines 200

# Database console
devopsellence exec web -- ./bin/rails dbconsole --include-password

# Node diagnostics
devopsellence node diagnose prod-1
devopsellence node logs prod-1 --lines 200
```

Create a redacted support bundle when handing context to another operator or agent:

```bash
devopsellence support bundle --output ./devopsellence-support.json
DEVOPSELLENCE_ENVIRONMENT=staging devopsellence support bundle --output ./devopsellence-support-staging.json
```

## Notes for production Fizzy instances

- Back up every VM volume that backs Fizzy storage; for this guide that means both `fizzy_production_storage`
  and `fizzy_staging_storage` if you deploy both environments.
- Use real SMTP credentials before inviting users. Passwordless login and notifications depend on mail.
- Keep each environment's `BASE_URL` aligned with its public HTTPS origin.
- If you enable multi-tenant signup, set `MULTI_TENANT=true` intentionally and review Fizzy's product-level
  account/signup expectations.
- This guide keeps the default SQLite/local-storage shape. If you later move to external object storage,
  MySQL, or more job workers, model each dependency explicitly instead of hiding it in shell hooks.
