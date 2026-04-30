---
name: solo-improvement-loop
description: Run long autonomous devopsellence solo mode improvement loops for public release and production readiness. Use when the user asks an AI coding agent to work for hours improving solo mode, polish UX/devx, release preview artifacts, dogfood official builds, fix findings, open stacked PRs, and repeat until devopsellence solo is ready for public use.
---

# Solo Improvement Loop

## Mission

Act like a co-founder/owner. Improve devopsellence solo mode until it is credible for public release and production use. Keep momentum, evidence, and cleanup discipline. Prefer small stacked PRs over large hidden batches.

Use this skill with `dogfood-solo`; do not duplicate its full QA matrix. Load `.agents/skills/dogfood-solo/SKILL.md` when live solo dogfood or release-readiness validation begins.

## Loop

1. Establish branch, base, PR stack, current SHA, and dirty worktree. Do not overwrite unrelated changes.
2. Check open PR/review/CI state before new work. If a prior stacked PR exists, continue from its head unless the user says otherwise.
3. Choose the feedback path:
   - For CLI-only changes that do not touch `agent/`, `deployment-core/`, release packaging, install scripts, or public artifact behavior, use `mise run release:cli:local` for the fast local dogfood loop.
   - For agent/core/release/install changes, or any final release-readiness claim, release through `component-release.yml` and install official artifacts.
4. If using GitHub release, verify the release tag points at the intended SHA.
5. Install the matching artifact for the chosen path. Never substitute a local CLI build for final release-readiness claims.
6. Verify checksums and binary versions for official artifacts. For the agent, use `-version`/`--version`, not a `version` subcommand.
7. Dogfood solo mode with `dogfood-solo`: first-run UX, node lifecycle, deploy, status, logs, secrets if relevant, rollback, detach/remove, cleanup.
8. Include at least one adversarial solo scenario when possible: failed healthcheck, stale desired-state/status, co-hosted environments on one node, agent restart, rollback, and detach cleanup.
9. Spawn fresh QA subagents when available. Give them the skill path, artifact/version/SHA, and a bounded QA focus. If subagents fail or quota out, continue locally and note the gap.
10. Triage findings:
   - `blocker`: data loss, unsafe cleanup, broken deploy/status/rollback, leaked secret, stranded resource, release/install failure.
   - `release`: confusing public UX, wrong exit code, bad next step, missing cleanup/rollback evidence, flaky solo e2e.
   - `polish`: wording, docs, diagnostics, non-critical ergonomics.
11. Fix root causes in small commits. Prefer tests that preserve the release-readiness ratchet.
12. Open or update a stacked PR. Keep scope tight and explain dogfood evidence.
13. Address review threads. Resolve threads after fixes land. Request Copilot review after each pushed PR update:

```sh
gh pr edit <pr-number> --add-reviewer copilot-pull-request-reviewer
```

14. Repeat from release on the new branch/head until the exit criteria say to stop.

## Exit Criteria

Stop and summarize when any hard cap is reached:

- 5 hours elapsed in the current autonomous run, unless the user explicitly asked for a longer duration.
- 10 PRs opened or updated in the current loop, unless the user explicitly raised the cap.
- Credentials, quota, missing approval, or destructive cleanup risk blocks meaningful progress.

Stop early and recommend release when all readiness criteria are met:

- Official release artifacts install cleanly, checksums verify, and binaries report the expected version/SHA.
- Solo CLI core lifecycle is feature-complete for public preview: init/config, node add/remove, deploy, status, logs/diagnostics, secrets where supported, rollback, detach, and cleanup.
- Dogfood covers first-run and adversarial paths with no blocker or release-class findings.
- UX/devx is polished enough for public use: clear commands, actionable errors, accurate exit codes, useful next steps, no hidden local-build assumptions.
- CI, relevant local tests, release workflow, and live cleanup evidence are green.
- Remaining work is documented as non-blocking polish or future roadmap.

If the cap is reached with open blocker or release-class findings, do not call the product ready. Leave a prioritized continuation plan and the exact next command.

## Evidence

Record enough detail in the thread or PR for another agent to continue without rediscovery:

- branch, PR number, base, head SHA
- release workflow URL and result
- release tag target SHA, asset names, checksum result, binary version output
- run directory
- VM/provider resource names, public IP/hostnames, cleanup result
- commands that found bugs
- tests run locally and in GitHub Actions
- open risks and skipped surfaces

## Autonomy Rules

- Keep working unless blocked by credentials, quota, destructive cleanup risk, or user direction.
- Prefer public-boundary-safe artifacts; do not commit secrets, private infra, tenant data, or live credentials.
- Make decisions in favor of simple, reliable solo mode over compatibility shims.
- Preserve ordinary-tool escape hatches: SSH, Docker, files, logs, JSON, cloud CLIs.
- If cleanup may delete unknown resources, stop and ask.
- If a release artifact is stale or mismatched, treat the release as failed even when local tests pass.
- If the fast path uses `mise run release:cli:local`, label the evidence as local CLI-loop evidence and run one official artifact pass before recommending release.
- If a live run creates infrastructure, cleanup must be verified before calling the loop complete.
- If a command exits nonzero as part of a negative test, capture why that exit is expected and make the harness explicit.
- If GitHub Actions or release publishing takes several minutes, keep watching; do not assume success from queued/in-progress state.

## Lessons From Prior Runs

- Review comments can identify real release blockers after dogfood passes. Fetch unresolved review threads before deciding the next fix.
- Status can look healthy for the selected runtime environment while top-level desired state is stale. Test stale current and stale co-hosted desired-state revisions.
- Co-hosted services must keep reconciling when a peer service is unhealthy. Test app A failure while app B remains served.
- Rollback and detach are release-readiness features, not cleanup afterthoughts. Validate rollback dry-run messaging, rollback success, detach state updates, and final node removal.
- Subagents may fail from quota. Treat that as reduced QA coverage, not a blocker by itself; continue with local evidence.
- A passed release workflow is not enough. Check tag SHA, release target, asset list, checksums, and binary-reported version.
- Handoff quality matters after long sessions. Write exact SHA, branch, PR, run directory, VM/IP, release URL, cleanup status, and next command.

## Useful Commands

```sh
gh workflow run component-release.yml --ref <branch> \
  -f source_ref=<branch> \
  -f version=v0.2.0-preview \
  -f release_kind=prerelease

gh run watch <run-id> --exit-status
gh release view v0.2.0-preview --json tagName,targetCommitish,isPrerelease,publishedAt,assets
gh release download v0.2.0-preview --repo devopsellence/devopsellence --pattern 'cli-linux-amd64' --pattern 'cli-SHA256SUMS' --pattern 'agent-linux-amd64' --pattern 'agent-SHA256SUMS'
sha256sum -c cli-SHA256SUMS --ignore-missing
sha256sum -c agent-SHA256SUMS --ignore-missing
mise run release:cli:local
```

## Handoff

When stopping or compacting, leave a terse handoff:

- current branch/PR/base/head
- release/version/SHA status
- dogfood run dir and cleanup state
- findings fixed, findings still open
- test/check status
- exact next command
