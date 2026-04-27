---
name: dogfood
description: Use when asked to dogfood devopsellence from an agent-mediated perspective: an AI/operator agent acting for a user who delegates deployment, diagnosis, cleanup, and reporting. The skill guides blind-pass and expert-pass testing with evidence, agent-centric scenarios, rubrics, and repeatable run artifacts.
---

# Dogfood

## Purpose

Run devopsellence as an AI/operator agent acting on behalf of a user. Find bugs, product gaps, confusing contracts, missing docs, unsafe workflows, and DevX friction that prevent an agent from deploying, observing, diagnosing, recovering, and explaining devopsellence operations.

Dogfood is not direct human CLI QA. It asks: can a user safely delegate the job to an agent, can the agent determine the right operations, can the agent execute them non-interactively where appropriate, can it understand what happened from structured/inspectable outputs, can it recover from failure, and can it explain the result back to the user?

Direct human UX matters only when it affects agent-mediated use, for example approval boundaries, docs the agent must read, terminal-only flows the agent cannot automate, ambiguous output the agent must parse, or messages the agent must relay to a user.

## Core Rules

- Write `devopsellence` lowercase.
- Evaluate from the agent-mediated perspective by default: the agent receives a user goal, operates devopsellence, asks the user for approvals or missing facts, and reports back with evidence.
- Do not score direct human terminal ergonomics as the primary product experience unless the user explicitly asks for human-direct QA.
- Prefer fresh temp apps and fresh state.
- For ordinary solo-mode node tests, prefer `zirk` VMs when available. Use `zirk health`, `zirk flavors`, `zirk create <run-scoped-name> --flavor <flavor>`, `zirk show <run-scoped-name>`, and `zirk exec <run-scoped-name> ...` as setup evidence.
- Use run-scoped names for external resources, for example `dogfood-<timestamp>` or the run slug. Do not use generic production-like names unless the scenario specifically requires testing that name.
- Start with a blind pass unless the user explicitly asks for code review first.
- During blind pass, use only context an external agent could use: public website docs, README/docs that match the target version when available, installed CLI help, API/JSON output, web UI state when unavoidable, generated errors, logs surfaced by the product, and ordinary tools exposed to the operator.
- If versioned public docs are unavailable, record the docs source used and treat any version mismatch as a finding instead of silently mixing sources.
- Do not read implementation source during blind pass.
- After blind pass, run expert pass: inspect source, logs, DB, tests, and root causes.
- Capture evidence: exact commands, key output excerpts, machine-readable payload excerpts, paths, screenshots when UI matters, user approval points, and time-to-first-agent-confidence.
- Separate product gaps from bugs. Product completeness and agent DevX count even when code works.
- Do not hide setup pain. Record missing machine-readable affordances, unclear next actions, prompts that block automation, slow feedback, scary output, and cleanup uncertainty.
- Keep secrets and private identifiers out of reports.

## Agent-Mediated Evaluation Frame

For every run, model three roles:

- Delegating user: owns the goal, constraints, approvals, and risk tolerance.
- Operator agent: reads docs/help/output, executes commands, interprets state, asks for approval when needed, and explains results.
- devopsellence: product under test.

Evaluate whether the operator agent can:

- discover the correct workflow without source inspection;
- run it non-interactively or identify explicit approval boundaries;
- get structured output (`--json` or equivalent) for plans, status, errors, logs, cleanup, and diagnostics where possible;
- rely on deterministic exit codes and stable error categories;
- understand intended state vs observed state without scraping styled terminal output;
- detect secrets/private identifiers and avoid leaking them;
- produce a concise, trustworthy report for the delegating user;
- recover or safely stop when information, credentials, approvals, or infrastructure are missing.

## Safety and Cleanup

- Before creating any VM, cloud resource, or long-lived local state, record the expected resource name, owner, approval requirement, and cleanup command in `commands.log`.
- Prefer ephemeral local or zirk-backed resources for routine dogfood runs.
- Avoid provider-created resources unless the scenario explicitly requires them and the user has approved the cost/blast radius.
- Always perform cleanup or document why cleanup was not possible.
- Verify cleanup with agent-usable commands, for example status/list commands, `zirk show`, or cloud/provider CLIs when used.
- If cleanup fails, mark the run as needing follow-up and include exact remaining resource identifiers after redaction.

## Command Logging

Keep `commands.log` as you go. For each meaningful step, record:

```text
## <ISO-8601 timestamp> <short step name>
cwd: <working directory>
agent intent: <why the agent is doing this>
user approval: <not needed | requested | granted | denied>
command: <command with secrets redacted>
exit: <exit code>
output excerpt:
<minimal stdout/stderr or JSON proving the result>
agent interpretation:
<what the agent concluded and next action>
```

Use placeholders such as `$TOKEN`, `<redacted>`, or `<private-host>` instead of secret values or private identifiers.

## Workflow

1. Pick scenario.
   - If the user names one, use it.
   - Otherwise choose the smallest agent-mediated scenario that answers the request.
   - Read `references/scenarios.md` only when scenario detail is needed.

2. Create run artifact.
   - Prefer `ruby .agents/skills/dogfood/scripts/new_run.rb <scenario>` from repo root; multi-word scenarios may be quoted or passed as multiple words.
   - If the user names a devopsellence version, pass `--version <version>`.
   - If no version is named, omit `--version` and dogfood the default stable installer/control-plane version.
   - Use the temp run path printed by the helper unless the user asks for repo-tracked reports.
   - Keep `commands.log` as described above.

3. Blind pass.
   - Act as the operator agent, not as a direct human user.
   - Use docs, CLI help, structured output, UI when unavoidable, ordinary tools, and terminal feedback.
   - Do not inspect source.
   - Install the target recorded in the run artifact: preview versions use `curl -fsSL https://www.devopsellence.com/lfg.sh?version=<version> | bash`; default stable uses `curl -fsSL https://www.devopsellence.com/lfg.sh | bash`.
   - For solo first-deploy scenarios, if `zirk` is installed and healthy, create a fresh run-scoped VM and use it as the existing SSH node. Record `zirk` commands in `commands.log`. Prefer the smallest flavor that can run Docker builds/deploys reliably.
   - Work from the delegating user's goal, constraints, and approvals; do not use privileged implementation knowledge.
   - Stop for missing approval, unsafe ambiguity, or hard blockers; otherwise recover like a competent operator agent would.

4. Agent-primary checks.
   - Probe whether the workflow can run non-interactively after required user approvals are known.
   - Prefer structured output when available, especially `--json` or equivalent.
   - Record whether errors include stable codes, machine-readable fields, suggested next actions, and deterministic exit codes.
   - Note any flow that requires scraping styled terminal text, prompts, spinners, browser-only state, or implicit TTY behavior.
   - Check whether the agent can produce a correct user-facing summary without guessing.

5. Expert pass.
   - Inspect implementation only after blind evidence is recorded.
   - Root-cause failures and confusing behavior.
   - Check whether existing tests cover the risk.

6. Score and report.
   - Read `references/rubric.md` for scoring when needed.
   - Use `references/report-template.md` for final structure.
   - Lead with outcome, top fixes, and evidence from the agent-mediated run.

7. Optional fixes.
   - If user asks to fix findings, make small reviewable changes.
   - Preserve unrelated worktree changes.
   - Re-run the relevant scenario or narrower verification.

## Scenario Defaults

Use these default delegating-user personas:

- Solo founder delegating deployment: wants one containerized app live on one VM and expects the agent to handle operational details safely.
- Rails developer delegating production config: understands app code, asks the agent to wire secrets/deploy/redeploy without leaking values.
- Infra-aware skeptic supervising an agent: checks plans, logs, rollback/delete, status, and escape hatches before approving risky actions.
- Tired maintainer delegating incident recovery: gives the agent a broken deploy and needs clear diagnosis, recovery, and stop/ask behavior.

Good default devopsellence journeys:

- Agent-mediated solo first deploy for a fresh Rails app.
- Agent-mediated existing app deploy with secrets.
- Agent-mediated failed deploy diagnosis and recovery.
- Agent-mediated status/log inspection and user summary after deploy.
- Agent-mediated delete/cleanup after experiment.
- Agent-mediated shared flow: connect node, deploy app, inspect status.
- Non-interactive deploy/status/log workflow with structured output.

## Evidence Standard

For each important claim, include one of:

- Command and output excerpt.
- JSON/API payload excerpt or schema shape.
- File path and line reference.
- UI screenshot or described visual state when UI is unavoidable.
- Log line with timestamp when available.
- Reproduction steps in agent-facing terms.
- User approval point and resulting agent action.

Do not over-quote logs. Keep enough to prove the point.

## Finding Standard

For each top finding, include:

- Severity: blocker, high, medium, low.
- Surface: docs, CLI, API, control plane, agent, deploy core, installer, cleanup, or observability.
- Expected agent-mediated behavior.
- Actual behavior.
- Reproduction evidence.
- Suggested fix.

## Output Shape

Final response should be short unless user asks for the full report inline:

- run path
- outcome from the agent-mediated perspective
- top 3 findings
- validation done
- next suggested fix batch

Write the full Markdown report to the run artifact.
