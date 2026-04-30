---
title: Secrets
description: Secret handling in solo and shared mode.
---

Application config should refer to secrets by name. Secret values should live in
a local operator-controlled store, an external password manager, or a shared
secret manager depending on mode.

Set a solo secret from standard input:

```bash
printf '%s' "$RAILS_MASTER_KEY" | devopsellence secret set RAILS_MASTER_KEY --service web --stdin
devopsellence secret list
```

By default, solo plaintext secrets are stored in the local solo state file with
`0600` permissions. This fits single-operator SSH workflows, not shared team
secret management.

Use an external reference when you do not want plaintext in local state:

```bash
devopsellence secret set DATABASE_URL --service web --store 1password --op-ref "$OP_REF"
```

Use shared mode when server-side encrypted team secrets and deploy tokens are
part of the workflow.
