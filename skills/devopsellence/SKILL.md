---
name: devopsellence
description: Use the devopsellence CLI to choose solo or shared workspace mode, deploy the current app, inspect status, and manage secrets or nodes.
homepage: https://www.devopsellence.com
---

# devopsellence

Use this skill when the user wants to deploy an app with devopsellence, check deployment state, manage secrets, register and attach their own nodes, or edit the main lifecycle-hook config.

## Default flow

1. Work in the app directory the user wants to deploy.
2. Check whether the CLI is already available:

```sh
command -v devopsellence
```

If the command is missing, tell the user the devopsellence CLI is required and point them to the official docs. Do not run setup scripts from this skill.

3. Choose the workspace mode before initializing:

- Use `solo` for SSH-first single-operator workflows, existing VMs, local node state, local secrets, and direct Docker image streaming over SSH.
- Use `shared` for browser sign-in, org/project/env context, hosted APIs, team workflows, shared encrypted secrets, and control-plane-managed nodes.
- If the user has not picked a mode and intent is unclear, ask. Do not silently choose one.

Inspect any existing mode/context when present:

```sh
devopsellence mode show || true
devopsellence context show || true
```

4. Initialize the workspace before the first deploy. For solo:

```sh
devopsellence init --mode solo
```

For shared:

```sh
devopsellence auth whoami || devopsellence auth login
devopsellence init --mode shared
```

If the user already knows the shared target workspace values, prefer explicit flags:

```sh
devopsellence init --mode shared --org acme --project shop --env staging
```

5. Validate local state after initialization and before deploy:

```sh
devopsellence doctor
```

6. Deploy the app:

```sh
devopsellence deploy
```

In shared mode, if the user wants to deploy an existing image digest instead of building locally:

```sh
devopsellence deploy --image docker.io/example/app@sha256:...
```

7. Verify the result:

```sh
devopsellence status
```

## CLI output contract

The devopsellence CLI is agent-primary. Treat stdout as the machine-readable contract.

- Bounded commands emit one JSON document on stdout.
- Long-running commands emit newline-delimited JSON events on stdout.
- Do not scrape human prose from stderr unless diagnosing a CLI/runtime failure.
- Every payload includes `schema_version`; check it before relying on fields.
- For schema version 1, streaming events use a common envelope:
  - `event`: `started`, `progress`, `result`, or `error`
  - `operation`: stable operation name when available
  - final success events include `ok: true`
  - final failure events include `ok: false` and `error`
- Structured errors use `error.code`, `error.message`, and `error.exit_code`.
- Command-specific fields are top-level. Tolerate unknown fields, but do not assume undocumented fields are stable.
- Prefer explaining failures from structured `code`, `message`, evidence fields, and suggested next actions when present.
- If `schema_version` is missing or unsupported, do not make high-risk decisions from command-specific fields; summarize the raw structured output and ask for updated docs or skill guidance.
- Keep secret values out of logs and chat output.

## Secrets

Prefer stdin over literal secret values in prompts or shell history:

```sh
printf '%s' "$VALUE" | devopsellence secret set NAME --service web --stdin
devopsellence secret list --env production
devopsellence secret delete NAME --service web
```

In solo mode, 1Password references are also supported:

```sh
devopsellence secret set NAME --service web --store 1password --op-ref op://vault/item/field
```

## Bring your own node

Use these in shared mode for a provider-created node. By default, `node create` registers and attaches the node to the current environment:

```sh
devopsellence init --mode shared
printf '%s' "$HCLOUD_TOKEN" | devopsellence provider login hetzner --stdin
# or: devopsellence provider login hetzner --token "$HCLOUD_TOKEN"
devopsellence node create prod-1 --provider hetzner
devopsellence node list
devopsellence node detach <id>
devopsellence node remove <id>
```

If you intentionally create an unassigned shared node, attach it later:

```sh
devopsellence node create prod-1 --provider hetzner --unassigned
devopsellence node attach <id>
```

Use this in shared mode for an existing server that you will install manually:

```sh
devopsellence node register
```

Use these in solo mode when the user wants SSH-first workflows without the control plane on an existing VM:

```sh
devopsellence init --mode solo
devopsellence node create prod-1 --host <ip> --user root --ssh-key ~/.ssh/id_ed25519
devopsellence agent install prod-1
devopsellence node attach prod-1
devopsellence doctor
devopsellence deploy
```

Use these in solo mode for a provider-created node:

```sh
devopsellence init --mode solo
printf '%s' "$HCLOUD_TOKEN" | devopsellence provider login hetzner --stdin
# or: devopsellence provider login hetzner --token "$HCLOUD_TOKEN"
devopsellence node create prod-1 --provider hetzner --install --attach
devopsellence doctor
devopsellence deploy
```

For solo diagnostics:

```sh
devopsellence status
devopsellence logs --node prod-1 --lines 100
devopsellence node diagnose prod-1
devopsellence node logs prod-1 --lines 100
```

For solo cleanup:

```sh
devopsellence node detach prod-1
devopsellence agent uninstall prod-1 --yes
devopsellence node remove prod-1 --yes
```

## Lifecycle hooks

When the user is editing `devopsellence.yml`, recognize these deploy-time hooks:

- `tasks.release`: one-shot task that runs before rollout. Good for migrations. It reuses the configured service image, env, secrets, and volumes.
- For per-node prep work, prefer the image entrypoint or boot-time scripts; the config-level `init` hook is no longer supported.

## Heuristics

- Prefer `devopsellence doctor` after init and before `devopsellence deploy`.
- If Docker is missing or not running, surface the problem clearly. In shared mode, switch to `devopsellence deploy --image ...` when the user already has a pushed image digest.
- If the workspace is not a git checkout and the CLI needs git metadata, stop and ask before creating a repo or commit.
- Keep secrets out of logs and chat output. Use environment variables plus `--stdin`.
