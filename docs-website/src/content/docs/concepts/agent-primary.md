---
title: AI operator model
description: Built for AI coding and operations assistants, transparent for humans.
---

devopsellence is AI-operator-first: the primary operator is an AI coding or
operations assistant acting for a human. Humans can still call the CLI directly,
approve plans, inspect JSON, SSH into nodes, read files, use Docker, and recover
with ordinary tools.

In these docs, **AI operator** means the AI assistant using devopsellence on a
human's behalf. **Node agent** means the daemon running on a VM.

## Why This Model Matters

Traditional deployment tools often assume a human is reading prose logs,
remembering the right follow-up command, and manually wiring several systems
together. AI operators can help, but only when the tools they call expose
explicit state, safe boundaries, and actionable next steps.

devopsellence makes that the product contract. The CLI should tell the AI
operator what happened, what is still pending, what failed, which action is safe
to take next, and which facts should be passed to another tool.

AI-operator-first operations need:

- JSON output by default;
- no TTY dependency on the happy path;
- no prompts as the core control surface;
- stable operation names and error codes;
- deterministic exit codes;
- explicit plan, dry-run, apply, rollback, and approval boundaries;
- structured findings with evidence and next safe actions;
- redacted secrets and logs by default;
- idempotent operations AI operators can safely retry.

The rule: AI operators should not invent live production mutations. They should
inspect config, produce plans, publish desired state after approval, observe
node-agent reconciliation, and propose repairs through devopsellence contracts.

## Compose With Ordinary Tools

AI-operator-first does not mean devopsellence owns every cloud integration. It
means devopsellence exposes deployment truth clearly enough for an AI operator to
compose it with the rest of the user's stack.

For example, a user can ask an AI operator to deploy an app and point a hostname
at it. The operator can:

1. run `devopsellence deploy`;
2. read the structured result for node IPs, ingress state, warnings, and DNS
   requirements;
3. call the user's DNS provider through an ordinary CLI or API;
4. run `devopsellence ingress check --wait` to wait for DNS and TLS readiness;
5. verify the public URL with an HTTP check;
6. report the final URL, evidence, and any remaining caveats.

The boundary stays narrow: devopsellence deploys and reconciles containerized
apps on VMs, while AI operators combine it with DNS, GitHub Actions, secret
stores, monitoring, incident tools, and other tools the user already has.

## Transparent For Humans

The same contracts that help AI operators also help humans. JSON output,
dry-run plans, stable error codes, and explicit next actions make it easier to
review what happened, approve risky steps, reproduce a workflow locally, and
recover with SSH, Docker, files, and logs when automation is not enough.
