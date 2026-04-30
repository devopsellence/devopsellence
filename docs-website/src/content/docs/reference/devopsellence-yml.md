---
title: devopsellence.yml
description: Application configuration reference.
---

`devopsellence.yml` lives in the app root. It describes workload config, not
local node inventory.

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
environments:
  production:
    services:
      web:
        env:
          RAILS_ENV: production
```

## Key fields

| Field | Purpose |
| --- | --- |
| `schema_version` | Config format version. |
| `organization` | Ownership scope. In solo examples this can be `solo`. |
| `project` | Application/project name. |
| `default_environment` | Environment selected when no override is provided. |
| `build` | Docker build context, Dockerfile, and target platforms. |
| `services` | Named runtime units. Each HTTP service needs ports and a health check. |
| `tasks.release` | Optional one-shot release command. |
| `ingress` | Hostnames, route rules, TLS behavior, and HTTP redirects. |
| `environments` | Per-environment overlays. |

Services are explicit. Do not rely on fixed concepts such as one `web` and one
`worker`; name the runtime units your app actually needs.
