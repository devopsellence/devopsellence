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

3. Validate local state before changing anything:

```sh
devopsellence doctor
```

4. Choose the workspace mode before the first setup:

```sh
devopsellence mode use shared
```

Then prepare the project:

```sh
devopsellence setup
```

If the user already knows the target workspace values, prefer explicit flags:

```sh
devopsellence mode use shared
devopsellence setup --org acme --project shop --env staging
```

5. Deploy the app:

```sh
devopsellence deploy
```

If the user wants to deploy an existing image digest instead of building locally:

```sh
devopsellence deploy --image docker.io/example/app@sha256:...
```

6. Verify the result:

```sh
devopsellence status --json
```

## Secrets

Prefer stdin over literal secret values in prompts or shell history:

```sh
printf '%s' "$VALUE" | devopsellence secret set NAME --service web --stdin
devopsellence secret set NAME --service web --store 1password --op-ref op://vault/item/field
devopsellence secret list --env production
devopsellence secret delete NAME --service web
```

## Bring your own node

Use these in shared mode when the user wants to run on their own machine or VM:

```sh
devopsellence mode use shared
devopsellence provider login hetzner
devopsellence node create prod-1
devopsellence node register
devopsellence node list --json
devopsellence node attach <id>
devopsellence node detach <id>
devopsellence node remove <id>
```

Use these in solo mode when the user wants SSH-first workflows without the control plane:

```sh
devopsellence mode use solo
devopsellence provider login hetzner
devopsellence node create prod-1
devopsellence setup
devopsellence deploy
devopsellence node logs <name> --follow
```

## Lifecycle hooks

When the user is editing `devopsellence.yml`, recognize these deploy-time hooks:

- `tasks.release`: one-shot task that runs before rollout. Good for migrations. It reuses the configured service image, env, secrets, and volumes.
- For per-node prep work, prefer the image entrypoint or boot-time scripts; the config-level `init` hook is no longer supported.

## Heuristics

- Prefer `devopsellence doctor` before `devopsellence deploy`.
- If Docker is missing or not running, surface the problem clearly, or switch to `devopsellence deploy --image ...` when the user already has a pushed image digest.
- If the workspace is not a git checkout and the CLI needs git metadata, stop and ask before creating a repo or commit.
- Keep secrets out of logs and chat output. Use environment variables plus `--stdin`.
