---
title: Secrets
description: Secret handling in solo and shared mode.
---

Application config should refer to secrets by name. Secret values should live in
a local operator-controlled store, an external password manager, or a shared
secret manager depending on mode.

## Solo mode

For production apps in solo mode, prefer 1Password references instead of storing
plaintext secrets in devopsellence's local solo state. This keeps secret values
out of devopsellence state at rest while preserving the SSH-first, single-operator
workflow.

```bash
devopsellence secret set DATABASE_URL --service web --store 1password --op-ref "op://vault/item/field"
```

The operator machine must have the 1Password CLI (`op`) installed and signed in.
During deploy, rollback, or desired-state republish, devopsellence runs
`op read` locally, resolves the value, and sends it to the node as runtime
environment. The node does not need access to 1Password.

For local development or low-risk apps, you can also store a solo plaintext
secret from standard input:

```bash
printf '%s' "$RAILS_MASTER_KEY" | devopsellence secret set RAILS_MASTER_KEY --service web --stdin
devopsellence secret list
```

Solo plaintext secrets are stored in the local solo state file with `0600`
permissions. This fits quick single-operator SSH workflows, but 1Password is the
safer default for production secrets.

## Shared mode

Use shared mode when server-side encrypted team secrets and deploy tokens are
part of the workflow.
