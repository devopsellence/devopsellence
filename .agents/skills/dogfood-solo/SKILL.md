---
name: dogfood-solo
description: >-
  Use when asked to dogfood or release-validate devopsellence solo mode from an AI-agent-mediated operator perspective. Focuses on official-artifact validation, co-hosted projects/environments, TLS/ACME, secrets, rollback, diagnostics, cleanup, and evidence-backed readiness decisions.
---

# Dogfood Solo

## Purpose

Run devopsellence **solo mode** as an AI coding/operator agent acting for a delegating user. The goal is not just to prove that one happy-path deploy works; it is to find runtime, state, release, and operability gaps that would cause an AI agent to report false confidence to a user.

Use this skill when validating solo-mode PRs, release candidates, GA readiness, or a suspected solo-mode regression. For broad product dogfood that is not solo-specific, use the generic `dogfood` skill. Future shared-mode validation should live in a separate `dogfood-shared` skill rather than diluting this one.

Terminology: "AI agent" means the coding/operator agent doing the dogfood run. "devopsellence node agent" means the runtime reconciler installed on the VM.

## Core Rules

- Scope this skill to `devopsellence init --mode solo` and solo node workflows.
- Evaluate from the AI-agent-mediated perspective: can an AI agent deploy, diagnose, recover, and explain without guessing?
- Prefer proof over CLI optimism. A command reporting success is not enough when a URL, container, node, secret, cert, or release can be checked directly.
- For release/RC validation, use **official CLI and agent artifacts from the exact commit/version**. A local CLI build does not prove the deployed node agent is current.
- Keep secrets out of terminal history, command logs, reports, PR bodies, and chat. Use stdin and redact values as `[REDACTED]`.
- Reuse existing dogfood nodes when the user approves and that better matches the scenario; otherwise prefer run-scoped disposable resources.
- Record every meaningful command and outcome in `commands.log` as the run proceeds.
- Every material finding must ratchet into at least one durable artifact: regression test, product issue, docs update, skill update, or follow-up PR.

## When to Use

Use this skill for:

- Solo-mode GA/release-readiness validation.
- PRs touching solo deploy, status, logs, exec, ingress, secrets, release history, rollback, node attach/detach, desired state, or agent reconciliation.
- Live validation of TLS auto/ACME, co-hosted projects, multiple environments on one node, secret isolation, rollback, dry-run boundaries, restart/reboot recovery, and cleanup.
- Investigating whether an AI agent can safely operate devopsellence solo without source-code knowledge.

Do not use this skill for:

- Shared/control-plane-first validation. Create/use `dogfood-shared` later.
- Pure browser UI QA.
- Unit-test-only verification with no live/runtime claim.

## Run Types

Pick the smallest run type that answers the user’s request.

### PR-focused solo probe

Use when a PR touches one solo-mode surface. Run focused tests plus the relevant live probe(s). It is acceptable to use a local CLI build if the PR only changes CLI-side behavior and the report clearly says so.

### RC/release solo validation

Use before calling a solo release candidate validated. Requirements:

1. Build or download official CLI and agent artifacts from the target commit/version.
2. Install the official CLI locally.
3. Install the official devopsellence node agent on every dogfood node.
4. Verify both binaries report the expected version/commit.
5. Force at least one fresh workload revision after artifact install.
6. Run the full high-value solo probe matrix.

### Incident/regression reproduction

Use when a user reports a solo-mode bug. Reproduce with evidence, isolate root cause after blind evidence is captured, add a regression test when fixing, and re-run the narrow live probe.

## Workflow

1. Create run artifact.
   - Prefer `mise exec -- ruby .agents/skills/dogfood-solo/scripts/new_run.rb <scenario>` from repo root when Ruby is managed by mise; plain `ruby ...` is fine when Ruby is already on `PATH`.
   - Use the printed run directory for `commands.log`, `report.md`, and evidence files.
   - If validating a version, pass `--version <version>`.
   - If validating a commit/PR, include the SHA/PR in the scenario name or report header.

2. Preflight.
   - Record repo branch/SHA, PR URL if any, target devopsellence version, and whether this is local-build or official-artifact validation.
   - Check `devopsellence --version` and, for live nodes, `/usr/local/bin/devopsellence-agent --version` or equivalent.
   - Identify the node strategy: existing approved node, zirk VM, provider-created VM, or user-provided SSH host.
   - Record resource names, expected cost/blast radius, approval state, and cleanup command before provisioning or destructive cleanup.

3. Prepare solo apps/environments.
   - At minimum use one app in `production`.
   - For co-hosting readiness, use a second project on the same node with a distinct hostname.
   - For multi-env readiness, use `production` and `staging` for one project on the same node with distinct hostnames.
   - Use hostnames that genuinely resolve to the node for TLS/DNS tests. IP-based wildcard DNS such as `x-x-x-x.sslip.io` is acceptable.

4. Run the high-value solo probe matrix.
   - Use `references/checklist.md` as the scenario checklist.
   - Prefer structured CLI output when available.
   - Verify runtime truth with curls, SSH/Docker observations, status/logs, and node-agent logs when appropriate.

5. Expert pass and fixes.
   - Only after blind/operator evidence is captured, inspect implementation, tests, state files, desired-state payloads, and node logs.
   - If fixing code, add regression coverage before or with the fix.
   - Re-run focused tests, broader relevant suites, and the narrow live probe that failed.

6. Report.
   - Use `references/report-template.md`.
   - Lead with verdict: ready, ready-with-known-gaps, blocked, or inconclusive.
   - Include what was actually verified, what was skipped, and why.
   - Link PRs/issues and evidence files.

## High-Value Solo Probe Areas

Treat these as the core solo readiness surfaces:

- **Official artifact reality:** CLI and node agent must both come from the intended version/commit for release validation.
- **TLS auto/ACME:** DNS correctness, HTTP-01 issuance, HTTPS curl success, HTTP→HTTPS redirect, restart/reuse, reboot recovery.
- **Manual TLS honesty:** do not count `tls.mode: manual` as dogfooded unless there is a documented cert/key provisioning path and live HTTPS proof.
- **Co-hosted projects:** two projects on one node with distinct hosts; verify routing, status, logs, exec, release list, rollback, detach/reattach.
- **Multiple environments:** production and staging on one node; verify env-specific ingress, secrets, releases, exec, status, and rollback.
- **Secret isolation:** same secret name across production, staging, and another project; verify values stay isolated without printing them.
- **Rollback semantics:** rollback should republish the stored historical release snapshot and should not mark the selected release current until publication succeeds.
- **Rollout freshness:** a settled status for the same desired-state revision can be stale; require fresh status evidence when available.
- **Dry-run boundary:** deploy/rollback dry-run must not build, publish, SSH, mutate release state, or write local state.
- **DNS honesty:** bad hostnames should produce clear structured missing-IP evidence rather than false readiness.
- **Diagnostics:** `status`, `logs`, `exec`, `node logs`, `node diagnose`, and `release list` should target the selected logical environment even when runtime environment names are project-scoped.
- **Cleanup/recovery:** detach/remove one co-hosted project without breaking others; restart agent; reboot node; verify all expected endpoints after recovery.

## Release Blockers

Block a solo release or mark the PR not ready if any of these are true:

- CLI reports success while endpoint/container/node status proves the deploy failed.
- HTTPS URLs are presented as ready before TLS is actually reachable.
- Current/effective environment is ignored by solo commands.
- Co-hosted projects/environments collide in runtime names, routes, secrets, status, logs, exec, releases, or rollback.
- Rollback mutates persistent current-release state before successful publication.
- Stale node-agent status can satisfy a fresh rollout.
- Dry-run performs side effects.
- Secret values leak in output, logs, release snapshots, reports, or command history.
- Cleanup cannot identify or remove resources created during the run.

## Finding Handling

For each finding, classify it as one of:

- **Bug:** implementation contradicts expected behavior; add or request regression coverage.
- **Product gap:** behavior may be intentional but blocks safe AI-agent operation; open an issue with acceptance criteria.
- **Docs gap:** workflow works but cannot be discovered from public docs/help/output.
- **Test gap:** behavior works now but lacks coverage for a class of regression.
- **Skill/process gap:** the dogfood process missed something; update this skill or references.

## Output Shape

Final response to the user should be concise:

- run path
- verdict
- top findings/blockers
- fixes/tests, if any
- live evidence summary
- PR/issue links
- remaining risks

Write the full report to the run artifact.
