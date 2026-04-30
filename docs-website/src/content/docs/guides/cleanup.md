---
title: Cleanup
description: Remove solo nodes and shared resources intentionally.
---

To clean up a solo experiment on an existing SSH node, detach the node, uninstall
the node agent, and forget the node locally:

```bash
devopsellence node detach prod-1
devopsellence agent uninstall prod-1 --yes
devopsellence node remove prod-1 --yes
```

`agent uninstall --yes` stops and disables `devopsellence-agent`, removes
devopsellence-managed containers, removes the Envoy container and Docker network,
deletes agent state, and removes `/usr/local/bin/devopsellence-agent`.

Use `--keep-workloads` only when you intentionally want to stop the node agent
without cleaning remote runtime resources.

Shared resource cleanup should happen through the control plane or shared CLI
commands so org/project/environment ownership remains auditable.
