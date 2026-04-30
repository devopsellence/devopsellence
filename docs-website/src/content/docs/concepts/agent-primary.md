---
title: Agent-primary operations
description: Built for AI coding and operations agents, transparent for humans.
---

devopsellence is agent-primary: the primary operator is an AI coding or
operations agent. Humans can still call the CLI directly, approve plans, inspect
JSON, SSH into nodes, read files, use Docker, and recover with ordinary tools.

Agent-primary operations need:

- JSON output by default;
- no TTY dependency on the happy path;
- no prompts as the core control surface;
- stable operation names and error codes;
- deterministic exit codes;
- explicit plan, dry-run, apply, rollback, and approval boundaries;
- structured findings with evidence and next safe actions;
- redacted secrets and logs by default;
- idempotent operations agents can safely retry.

The rule: AI agents should not invent live production mutations. They should
inspect config, produce plans, publish desired state after approval, observe
reconciliation, and propose repairs through devopsellence contracts.
