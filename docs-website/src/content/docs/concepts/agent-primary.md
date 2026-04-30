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
