# Dogfood Scenarios

All scenarios are AI-agent-mediated by default. The primary actor is an AI coding/operator agent acting on behalf of a delegating user. Do not evaluate devopsellence as direct human CLI UX unless the scenario explicitly asks for that.

Terminology: "AI agent" means the AI coding/operator agent doing the dogfood run. "devopsellence node agent" means the runtime reconciler on the VM.

## ai-agent-mediated-solo-rails-first-deploy

Delegating user: solo Rails founder, infra-aware but impatient.

AI coding/operator agent goal: deploy a fresh Rails app with devopsellence solo, ask for approval before creating resources, verify the app, summarize what changed, and clean up if this is an experiment.

Allowed blind-pass context: public website docs, README/docs that match the target version when available, CLI help, JSON/API output, command output, product logs/status surfaced by commands, ordinary SSH/Docker/file tools when exposed by the workflow.

Setup:

- Use a fresh app and fresh devopsellence state.
- If `zirk` is installed and healthy, create a run-scoped VM name such as `dogfood-<timestamp>` and use it as the existing SSH node.
- Record expected resource creation, required user approval, and cleanup before creating the node.

Success:

- The AI agent can discover the deployment workflow without source inspection.
- The AI agent can install/setup/deploy with clear approval boundaries.
- App reachability can be verified with evidence.
- Deploy status is machine-readable or inspectable enough for the AI agent to explain.
- Secret path is discoverable without leaking values.
- Logs/status path is discoverable.
- Delete or cleanup path is clear and verified.

Probe:

- Whether the first deploy can be run non-interactively after approvals are known.
- Whether plan/apply/status/logs have structured output.
- Whether errors tell the AI agent the next safe action.
- Time to first useful AI agent confidence.
- Confidence of the final user-facing summary.

## ai-agent-mediated-existing-app-secrets-redeploy

Delegating user: Rails developer adding production-like config.

AI coding/operator agent goal: deploy an existing app, add or update a secret, redeploy, verify status, and report back without exposing the secret.

Allowed blind-pass context: public website docs, README/docs that match the target version when available, CLI help, JSON/API output, command output, product logs/status surfaced by commands.

Success:

- Secret command or workflow is discoverable by the AI agent.
- The AI agent can determine required scope: app, environment, node, or service.
- Secret value does not leak in commands, logs, reports, or output excerpts.
- Redeploy makes intended vs active state understandable.
- Failed secret usage has clear machine-readable or clearly parseable recovery.
- Status identifies the active revision/config when available.

Probe:

- Naming of app/environment/secret scopes.
- Whether local and remote state are easy for the AI agent to distinguish.
- Whether status explains which revision/config is active.
- Whether secret workflows work non-interactively without leaking values.
- Whether the AI agent knows when to ask the user for a secret vs guessing.

## ai-agent-mediated-failed-deploy-recovery

Delegating user: tired maintainer at night.

AI coding/operator agent goal: diagnose and recover from a broken deploy, or stop safely with a clear request for missing approval/information.

Allowed blind-pass context: public website docs, README/docs that match the target version when available, CLI help, JSON/API output, command output, product logs/status surfaced by commands.

Failure seeds:

- Bad image or build command.
- Missing secret.
- Bad port.
- Unreachable node.
- App starts then exits.

Success:

- Failure is surfaced without source inspection.
- The AI agent can distinguish intended state from observed state.
- Next safe action is obvious or explicitly asks for user approval.
- Logs are reachable with bounded, relevant output.
- Retrying after fix is deterministic.
- Exit codes and structured errors are deterministic enough for automation.
- The final report explains cause, action taken, residual risk, and verification.

Probe:

- Error specificity and stable categories.
- Whether failed desired state is visible.
- Whether rollback/delete/cleanup is understandable.
- Whether JSON or other structured output avoids terminal-text scraping.
- Whether the AI agent avoids unsafe repair attempts without approval.

## ai-agent-mediated-shared-node-connect-deploy

Delegating user: user evaluating hosted/shared control plane.

AI coding/operator agent goal: connect a node, deploy an app, inspect status, and explain hosted vs local responsibilities.

Allowed blind-pass context: public website docs, hosted UI when required, CLI help, JSON/API output, command output, product logs/status surfaced by commands.

Success:

- Node enrollment is understandable to the AI agent.
- Hosted vs local responsibilities are clear enough to explain.
- Status reflects node/app state in an AI-agent-readable way.
- Escape hatches remain ordinary: SSH, Docker, logs, JSON.
- Cleanup/de-enrollment path is clear and verified.
- Browser-only or UI-only state does not block AI agent operation, or is reported as a gap.

Probe:

- Account/environment naming.
- devopsellence node agent reconciliation mental model.
- Trust boundary clarity.
- Structured operations for autonomous agents.
- Approval boundaries for enrollment, deploy, and cleanup.

## non-interactive-agent-workflow

Delegating user: human operator asking an AI coding/operator agent to manage devopsellence.

AI coding/operator agent goal: inspect, validate/plan or dry-run when available, deploy/apply when approved, read status/logs, and recommend recovery using deterministic commands.

Allowed blind-pass context: public website docs, CLI help, JSON/API output, command output, product logs/status surfaced by commands.

Success:

- Workflow can run without prompts or TTY-only behavior after approvals are known.
- Commands provide structured output suitable for parsing.
- Errors include stable codes or machine-readable categories when available.
- Exit codes are meaningful and deterministic.
- The AI agent can explain intended state, observed state, and next action without scraping styled text.
- The AI agent can produce a concise user report with evidence and residual risks.

Probe:

- `--json` or equivalent coverage.
- Non-interactive flags and defaults.
- Dry-run/plan/apply boundary clarity.
- Stable operation names and error shapes.
- Whether UI-only functionality has an API/CLI equivalent.
