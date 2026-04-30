---
title: Troubleshooting
description: First commands to run when a deploy or node is unhealthy.
---

Start with structured evidence:

```bash
devopsellence doctor
devopsellence status
devopsellence node diagnose prod-1
devopsellence support bundle --output ./devopsellence-support.json
```

Then inspect logs:

```bash
devopsellence logs --node prod-1 --lines 100
devopsellence node logs prod-1 --lines 100
```

Common next questions:

- What release is intended?
- What publication reached each node?
- What is actually running?
- Which health check failed?
- What should I inspect next with SSH, Docker, files, logs, JSON, or cloud CLIs?

devopsellence should keep ordinary operational tools useful. SSH into the node
when structured evidence says node-local inspection is the safer next step.
