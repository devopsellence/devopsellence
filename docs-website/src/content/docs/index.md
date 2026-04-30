---
title: devopsellence docs
description: Deploy and operate containerized apps on VMs you control.
template: splash
hero:
  tagline: Built for agents, transparent for humans.
  actions:
    - text: Start with solo
      link: /getting-started/solo-quickstart/
      icon: right-arrow
    - text: Read the concepts
      link: /concepts/runtime-model/
      icon: open-book
---

devopsellence is a VM-native deployment toolkit for containerized applications.
It gives you a small app config, a node agent, and a reconciliation loop instead
of a platform-owned runtime.

<img class="runtime-diagram" src="/devopsellence-runtime.svg" alt="devopsellence runtime model" />

## The short version

<div class="mode-grid">
  <section class="mode-panel">
    <h3>Solo</h3>
    <p>Use SSH, Docker, local state files, and your own VM. Best when one operator or one AI agent owns the deployment loop.</p>
  </section>
  <section class="mode-panel">
    <h3>Shared</h3>
    <p>Use the same app model with sign-in, org/project/environment context, hosted APIs, and team workflows.</p>
  </section>
</div>

The node agent is the mandatory runtime component. The CLI and control plane are
product surfaces around the same deployment model.
