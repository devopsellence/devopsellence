---
title: Agent-first DevOps
description: Why devopsellence is designed for AI operators, composable automation, and human recovery.
---

devopsellence is agent-first DevOps: the primary operator is an AI coding or
operations assistant acting for a human. Humans can still call the CLI directly,
approve plans, inspect JSON, SSH into nodes, read files, use Docker, and recover
with ordinary tools.

In these docs, **AI operator** means the AI assistant using devopsellence on a
human's behalf. **Node agent** means the daemon running on a VM.

## Why agent-first is attractive

Traditional DevOps tools assume the human is reading prose logs, remembering the
right follow-up command, and manually wiring several systems together. AI agents
can help, but only when the tools they call expose explicit state, safe
boundaries, and actionable next steps.

devopsellence makes that the product contract. The CLI tells the AI operator
what happened, what is still pending, what failed, which action is safe to take
next, and which facts should be passed to another tool.

That changes the user experience from:

> read docs, run commands, interpret logs, debug infrastructure

into:

> state intent, approve boundaries, let an AI operator orchestrate the workflow,
> and inspect the resulting evidence

## What the CLI gives AI operators

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

## Composable with the user's tools

Agent-first does not mean devopsellence needs to own every cloud integration.
It means devopsellence exposes deployment truth clearly enough for the user's AI
operator to compose it with the rest of their stack.

For example, a user can ask:

> Deploy this app with devopsellence and point `app.example.com` at it.

An AI operator can then:

1. run `devopsellence deploy`;
2. read the structured result for node IPs, ingress state, warnings, and DNS
   requirements;
3. call a DNS provider tool such as the Cloudflare CLI or API to create the
   required records;
4. run `devopsellence ingress check --wait` to wait for DNS and TLS readiness;
5. verify the public URL with ordinary HTTP checks;
6. report the final URL, evidence, and any remaining caveats.

That is the design advantage: devopsellence stays focused on deploying and
reconciling containerized apps on VMs, while AI operators can combine it with
Cloudflare, GitHub Actions, secret stores, monitoring, incident tools, or any
other ordinary CLI/API the user already uses.

## Transparent for humans

The same contracts that help AI operators also help humans. JSON output, dry-run
plans, stable error codes, and explicit next actions make it easier to review
what happened, approve risky steps, reproduce a workflow locally, and recover
with SSH, Docker, files, and logs when automation is not enough.
