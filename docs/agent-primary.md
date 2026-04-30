# agent-primary devopsellence

## stance

devopsellence is an agent-primary deployment toolkit for containerized apps on VMs you control.

The primary operator is an AI coding or operations agent. A human can still call the CLI directly, approve plans, inspect JSON, SSH into nodes, read files, use Docker, and recover with ordinary tools. But the product surface is designed first for agents that need narrow, structured, auditable operations rather than terminal-only workflows.

Short version:

> built for agents, transparent for humans.

## what changes

The CLI should not optimize for pretty terminal UX. It should optimize for agent-safe contracts:

- JSON output by default;
- no TTY dependency;
- no prompts as the happy path;
- stable operation names;
- stable machine-readable error codes;
- deterministic exit codes;
- explicit plan, dry-run, apply, rollback, and approval boundaries;
- structured findings with evidence and next safe actions;
- redacted secrets and logs by default;
- idempotent operations that agents can safely retry;
- correlation IDs, release IDs, publication IDs, node IDs, service IDs, and task IDs wherever applicable.

Human output, where it exists, should be a rendering of the same structured result. It should not be the source contract.

## core rule

AI agents should not invent live production mutations.

They should inspect config, produce or review plans, publish desired state after approval, observe reconciliation, and propose repairs through devopsellence contracts. The node agent remains deterministic. There is no LLM inside the runtime reconciler.

## product surfaces

The intended shape is:

```text
deployment core
  -> agent-primary CLI
  -> local MCP adapter
  -> hosted control-plane API/MCP
  -> node agent reconciler
```

The CLI remains useful locally, but it is a machine interface first. MCP should expose the same operations with tighter tool schemas and approval metadata. The hosted control plane should add auth, team policy, approvals, audit, fleet status, and paid infrastructure adapters without redefining deploy semantics.

## first CLI target

The clean-slate CLI direction is:

```sh
devopsellence inspect
devopsellence validate
devopsellence plan
devopsellence deploy --dry-run
devopsellence apply <plan-id>
devopsellence status
devopsellence doctor
devopsellence logs web
devopsellence release rollback --dry-run <release-id>
```

All of these should emit structured JSON without requiring `--json`.

## result shape

Command results should converge toward a common envelope:

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

## anti-goals

- do not put an LLM inside the node agent;
- do not make chat the deployment API;
- do not require humans to use an AI agent;
- do not give agents unrestricted shell as the happy path;
- do not keep terminal prompts, spinners, and TTY rendering as core CLI behavior;
- do not expose secret values through agent tools;
- do not invent a second deploy model for AI workflows.

## success test

devopsellence is agent-primary when an AI coding agent can safely answer and act on:

- inspect this repo and tell me if it can deploy;
- plan a production deploy and explain the blast radius;
- deploy after I approve the plan;
- why is production unhealthy?;
- what changed between the last good release and this one?;
- propose the safest rollback;
- show me what the node is actually running;
- generate a support bundle without exposing secrets.

The durable advantage is not AI branding. It is the combination of deterministic reconciliation, desired state as the write boundary, structured operational evidence, and narrow tools that let agents operate production safely.
