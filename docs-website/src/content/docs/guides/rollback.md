---
title: Rollback
description: Review and apply a rollback through explicit plan boundaries.
---

Rollback should follow the same safety shape as deploy: inspect first, approve
second, apply last.

```bash
devopsellence release rollback --dry-run <release-id>
devopsellence release rollback <release-id>
devopsellence status
```

The dry-run should explain the target release, affected environment, services,
nodes, and expected desired-state publication.

AI operators should present rollback plans to humans before mutating production
unless an explicit policy says otherwise.
