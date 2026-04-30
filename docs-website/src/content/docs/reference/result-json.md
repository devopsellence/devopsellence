---
title: Result JSON
description: Target result shape for AI-operator-first operations.
---

Command results should converge on a shared envelope:

```json
{
  "ok": false,
  "operation": "deploy.plan",
  "schema_version": 1,
  "app": "example",
  "environment": "production",
  "summary": "deployment cannot proceed",
  "findings": [
    {
      "severity": "error",
      "code": "missing_secret",
      "message": "DATABASE_URL is required by service web",
      "evidence": {
        "service": "web",
        "secret": "DATABASE_URL"
      },
      "next_actions": [
        {
          "label": "set secret reference",
          "command": "devopsellence secret set DATABASE_URL --service web --stdin"
        }
      ]
    }
  ]
}
```

Prefer stable operation names, stable machine-readable error codes, deterministic
exit codes, and redacted evidence. Human output should be a rendering of this
data, not the source contract.
