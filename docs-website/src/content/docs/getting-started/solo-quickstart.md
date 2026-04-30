---
title: Solo quickstart
description: Deploy a Dockerized app to a VM over SSH.
---

Solo mode keeps the deployment loop local: your checkout, your VM, SSH, Docker,
and the `devopsellence` CLI.

<ol class="command-sequence">
  <li>Initialize the workspace.</li>
</ol>

```bash
devopsellence init --mode solo
```

Start from an app that already has a Dockerfile. devopsellence does not install
language toolchains or generate Rails, Node, or Go apps for you.

<ol class="command-sequence" start="2">
  <li>Commit the app before the first deploy.</li>
</ol>

```bash
git init # if this is not already a checkout
git add .
git commit -m "initial deploy"
```

devopsellence uses the current git commit as the workload revision and image tag.

<ol class="command-sequence" start="3">
  <li>Add a node, install the agent, and attach it.</li>
</ol>

```bash
devopsellence node create prod-1 --host 203.0.113.10 --user root --ssh-key ~/.ssh/id_ed25519
devopsellence agent install prod-1
devopsellence node attach prod-1
devopsellence doctor
```

Existing SSH nodes need key-based SSH and Docker. On supported Ubuntu VMs,
`devopsellence agent install` can install Docker when it is missing.

<ol class="command-sequence" start="4">
  <li>Deploy, inspect, and read logs.</li>
</ol>

```bash
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
devopsellence logs --node prod-1 --lines 100
devopsellence node logs prod-1 --lines 100
```

`deploy --dry-run` prints a structured plan and does not build images, connect to
nodes, publish desired state, or write solo state. Review that plan before
mutating production.
