---
title: Deploy Flue Agents With Solo
description: Build a Flue Node.js agent server and deploy it to a VM with devopsellence solo.
---

[Flue](https://github.com/withastro/flue) builds agent workspaces into ordinary
HTTP servers. That makes it a useful fit for devopsellence: Flue owns the agent
runtime, while devopsellence owns the VM deployment loop, secrets, health
checks, logs, rollback, and TLS.

Flue is experimental and its APIs may change. This example was checked against
`@flue/sdk` and `@flue/cli` `0.3.10`; verify the Flue commands locally before
using this shape in production.

## Build The Flue App

Create a minimal webhook agent:

```bash
mkdir my-flue-server
cd my-flue-server
npm init -y
npm install @flue/sdk@0.3.10 @flue/cli@0.3.10 valibot@^1.0.0
mkdir -p .flue/agents
```

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

Check the Node target before adding devopsellence:

```bash
npx flue build --target node
PORT=8080 node dist/server.mjs
```

From another shell:

```bash
curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8080/agents
```

## Containerize It

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

```bash
cat > .dockerignore <<'EOF'
.git
.env
node_modules
dist
EOF
```

## Add Devopsellence Config

```bash
devopsellence init --mode solo
```

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

Store the provider key through devopsellence instead of committing `.env`:

```bash
printf '%s' "$OPENAI_API_KEY" | devopsellence secret set OPENAI_API_KEY --service web --stdin
```

## Deploy

Attach an existing VM:

```bash
devopsellence node create prod-1 --host <server-ip-or-hostname> --user root --ssh-key ~/.ssh/id_ed25519
devopsellence agent install prod-1
devopsellence node attach prod-1
```

Then publish desired state:

```bash
devopsellence doctor
devopsellence deploy --dry-run
devopsellence deploy
devopsellence ingress check --wait 5m
devopsellence status
```

Verify Flue's generated endpoints and one real agent route:

```bash
curl -fsS https://flue.example.com/health
curl -fsS https://flue.example.com/agents
curl -fsS https://flue.example.com/agents/translate/prod-smoke \
  -H "Content-Type: application/json" \
  -d '{"text":"Hello world","language":"French"}'
```

Node-target Flue sessions are in-memory by default. Add durable session storage
inside the Flue app before relying on session history surviving restarts.
