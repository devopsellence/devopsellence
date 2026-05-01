---
title: Deploy Flue agents with solo
description: Build a Flue Node.js agent server and deploy it to your own VM with devopsellence solo.
---

[Flue](https://github.com/withastro/flue) can compile an agent workspace into a normal Node.js server. That makes it a good fit for devopsellence: Flue owns the agent runtime, while devopsellence owns the VM deployment loop, secrets, health checks, logs, rollback, and TLS.

This guide deploys a small webhook-triggered Flue agent as a containerized Node.js service on one VM with `devopsellence solo`.

## What you will deploy

The deployed service exposes:

- `GET /health` from the generated Flue server
- `GET /agents` for the generated agent manifest
- `POST /agents/translate/:id` for a sample translation agent

The app stays ordinary on purpose: a `Dockerfile`, a `devopsellence.yml`, and secrets stored through the devopsellence CLI.

## Prerequisites

- Node.js and npm on your development machine
- the devopsellence CLI installed locally
- a VM reachable over SSH with Docker installed, or a provider-backed solo node created by devopsellence
- a DNS name such as `flue.example.com` pointing at the VM
- an LLM provider API key, such as `OPENAI_API_KEY`

## Create the Flue project

```bash
mkdir my-flue-server
cd my-flue-server
npm init -y
npm install @flue/sdk valibot
npm install -D @flue/cli
mkdir -p .flue/agents
```

Create the webhook agent:

```ts title=".flue/agents/translate.ts"
import type { FlueContext } from '@flue/sdk/client';
import * as v from 'valibot';

export const triggers = { webhook: true };

export default async function ({ init, payload }: FlueContext) {
  const agent = await init({ model: 'openai/gpt-5.5' });
  const session = await agent.session();

  return await session.prompt(`Translate this to ${payload.language}: "${payload.text}"`, {
    result: v.object({
      translation: v.string(),
      confidence: v.picklist(['low', 'medium', 'high']),
    }),
  });
}
```

For local development, keep provider keys in an uncommitted `.env` file:

```bash
cat > .env <<'EOF'
OPENAI_API_KEY="***"
EOF
printf '\n.env\n' >> .gitignore
```

Run it locally:

```bash
npx flue dev --target node --env .env
```

Then test the generated route from another shell:

```bash
curl -fsS http://localhost:3583/agents/translate/test-1 \
  -H "Content-Type: application/json" \
  -d '{"text":"Hello world","language":"French"}'
```

## Add a production Dockerfile

Flue's Node target builds to `dist/server.mjs`. The generated server reads `PORT` and uses `3000` by default. This Dockerfile sets `PORT=8080` so the internal service port is explicit in devopsellence.

```dockerfile title="Dockerfile"
FROM node:22-slim AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM deps AS build
COPY . .
RUN npx flue build --target node

FROM node:22-slim AS runtime
WORKDIR /app
ENV NODE_ENV=production
ENV PORT=8080

COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY --from=build /app/dist ./dist

EXPOSE 8080
CMD ["node", "dist/server.mjs"]
```

Keep local-only files out of the image and repository:

```bash
cat > .dockerignore <<'EOF'
.git
.env
node_modules
dist
EOF
```

## Add devopsellence config

Initialize solo mode if this is the first devopsellence deployment from the repo:

```bash
devopsellence init --mode solo
```

Then make the app config explicit. Replace `flue.example.com` and `ops@example.com` with your values.

```yaml title="devopsellence.yml"
schema_version: 1
organization: solo
project: flue-agent
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
        port: 8080
    healthcheck:
      path: /health
      port: 8080
    env:
      NODE_ENV: production
      PORT: "8080"
    secret_refs:
      - name: OPENAI_API_KEY
        secret: OPENAI_API_KEY

ingress:
  hosts:
    - flue.example.com
  rules:
    - match:
        host: flue.example.com
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

- Flue's production build is a single Node.js HTTP server.
- `/health` is the generated server health check.
- `OPENAI_API_KEY` is stored as a devopsellence secret instead of committed in `.env`.
- Node inventory stays outside `devopsellence.yml`; the app config only describes workload desired state.

## Store provider secrets

Prefer `--stdin` so secret values do not land in shell history:

```bash
printf '%s' "$OPENAI_API_KEY" | devopsellence secret set OPENAI_API_KEY --service web --stdin
```

You can also store a solo secret as a 1Password reference. The reference is saved locally; the value is resolved on your operator machine at deploy time:

```bash
devopsellence secret set OPENAI_API_KEY --service web --store 1password --op-ref op://deploy/flue/openai-api-key
```

## Attach a node

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

## Deploy

Check the workspace before applying changes:

```bash
devopsellence doctor
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
```

Verify the real endpoints, not just the CLI output:

```bash
curl -fsS https://flue.example.com/health
curl -fsS https://flue.example.com/agents
curl -fsS https://flue.example.com/agents/translate/prod-smoke \
  -H "Content-Type: application/json" \
  -d '{"text":"Hello world","language":"French"}'
```

If TLS is still pending, run the explicit ingress readiness check and then retry HTTPS:

```bash
devopsellence ingress check --wait 2m
curl -fsS https://flue.example.com/health
```

## Operate it

Useful day-two commands:

```bash
# Current deployment and node health
devopsellence status

# Web logs
devopsellence logs web --node prod-1 --lines 200

# Shell into the running service container
devopsellence exec web -- sh

# Inspect releases and roll back if needed
devopsellence release list
devopsellence release rollback

# Node diagnostics
devopsellence node diagnose prod-1
devopsellence node logs prod-1 --lines 200
```

Create a redacted support bundle when handing context to another operator or agent:

```bash
devopsellence support bundle --output ./devopsellence-support.json
```

## Sandbox and command safety

The example above uses Flue's default virtual sandbox, which is a good starting point for stateless webhook agents.

If you switch to Flue's `sandbox: 'local'`, the agent can access the host filesystem for the running server. That is useful for trusted single-tenant coding agents, CI helpers, and internal operations agents, but it is not isolation. For public or multi-tenant agents, use a container sandbox provider and treat any mounted environment variables as visible to the agent.

When an agent needs tools like `git`, `npm`, or `docker`, prefer Flue's command grants over broad environment exposure. Grant the smallest command surface for the prompt or skill that needs it, and keep secrets in the host process or in devopsellence secrets rather than writing them into the sandbox.

## Production notes

- Commit `package-lock.json`, `Dockerfile`, `.dockerignore`, and `devopsellence.yml`.
- Do not commit `.env` or provider API keys.
- Keep `PORT` in `devopsellence.yml` aligned with the Dockerfile.
- Add durable session storage in your Flue app before relying on sessions surviving restarts.
- Use app-aware smoke tests: `/health` and `/agents` exist for the generated server, but custom agent routes depend on the agents you define.
