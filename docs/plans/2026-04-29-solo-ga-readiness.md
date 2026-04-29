# Solo GA Readiness Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task, but keep high-risk runtime fixes under direct verification and dogfood every tranche with official CLI+agent artifacts.

**Goal:** Make solo mode GA-ready as the simplest serious system for running small-to-medium containerized apps on VMs users control.

**Architecture:** Preserve the vision/north-star: VM-first, agent-primary, transparent for humans, ordinary-tool debuggability, desired-state as the stable control surface, mode-independent runtime semantics. Prioritize correctness and operator trust over broad product expansion.

**Tech Stack:** Go CLI, Go node agent, Docker, Envoy, SSH, solo local state files, GitHub Actions component release, Ruby e2e harness.

---

## Product guardrails

A change is GA-aligned only if it strengthens at least one of these:

1. Agent-primary structured operation surfaces.
2. Runtime correctness and reconciliation reliability.
3. Transparent status/diagnostics explain reality without scraping terminal prose.
4. Ordinary tools remain valid escape hatches: SSH, Docker, files, JSON, cloud CLIs.
5. Solo/shared semantics converge rather than fork.
6. No new platform abstraction that hides VMs or duplicates provider/OS primitives.

## P0 tranche

### Task 1: Fix stale settled status suppression

**Objective:** Ensure the agent reports changed environment/service status even under the same desired-state revision/sequence.

**Files:**
- Modify: `agent/internal/agent/agent.go`
- Test: `agent/internal/agent/agent_test.go`

**Steps:**
1. Add a failing test where two settled statuses share authority/sequence/revision but have different `Environments` or service phase/state.
2. Verify it fails because the second report is suppressed.
3. Update `reportFingerprint.suppresses` or fingerprint construction so environment/status hash changes prevent suppression.
4. Run focused agent tests.
5. Commit.

**Verification:**
```bash
cd agent
mise x -- go test ./internal/agent -run 'Suppress|Report|Environment|Status' -count=1
mise x -- go test ./internal/... 
```

### Task 2: Make rollback publish historical release semantics

**Objective:** Rollback must restore the historical release snapshot, not current config plus old image/revision.

**Files:**
- Modify: `cli/internal/workflow/solo.go`
- Modify as needed: `cli/internal/solo/state.go`
- Test: `cli/internal/workflow/solo_test.go`

**Steps:**
1. Add a failing test: deploy release A with env/ingress/healthcheck shape A, change config to shape B, deploy B, rollback to A, assert desired-state publication matches A semantics.
2. Inspect release record persistence and desired-state snapshot storage.
3. Refactor rollback hydration to use the stored release/publication snapshot as source of truth.
4. Preserve secret resolution safety; do not expose secret values in release metadata.
5. Run focused CLI tests and full CLI tests.
6. Commit.

### Task 3: Make HTTPS/TLS readiness honest

**Objective:** Do not report HTTPS public URLs as ready when auto-TLS has not produced certs/listeners yet.

**Files:**
- Modify: `agent/internal/reconcile/reconcile.go`
- Modify: `agent/internal/envoy/manager.go` only if status needs listener evidence
- Modify: `cli/internal/workflow/solo.go`
- Tests: agent reconcile/envoy tests, CLI solo tests

**Steps:**
1. Add failing test for auto-TLS pending: deploy can settle HTTP fallback but status/URL output marks HTTPS pending or warning.
2. Add structured status fields/finding for TLS readiness without making transient ACME pending fatal.
3. Adjust CLI public URL generation to distinguish reachable URLs from configured/pending URLs.
4. Run focused and full tests.
5. Commit.

### Task 4: Standardize bounded solo result/error contract

**Objective:** GA solo commands consistently emit schema/version/operation/ok and stable error codes.

**Files:**
- Modify: `cli/internal/output/output.go`
- Modify: `cli/internal/workflow/solo.go`
- Modify: `cli/internal/workflow/rendered_error.go`
- Modify: `cli/cmd/devopsellence/main.go`
- Tests: `cli/internal/workflow/json_test.go`, `solo_test.go`, `root_test.go`

**Steps:**
1. Add contract tests for representative commands.
2. Add bounded result helper and coded error interface.
3. Migrate high-priority commands: status, doctor, release list, node attach/detach/remove, labels, secrets list/delete, logs, ingress set/check.
4. Preserve backwards-compatible command-specific fields.
5. Run CLI tests.
6. Commit.

### Task 5: Add dry-run boundaries for deploy and rollback

**Objective:** Agents can inspect a plan before mutating production.

**Files:**
- Modify: `cli/internal/workflow/root.go`
- Modify: `cli/internal/workflow/solo.go`
- Tests: `cli/internal/workflow/solo_test.go`, `root_test.go`

**Steps:**
1. Add failing tests for `deploy --dry-run` and `release rollback --dry-run` not writing state or publishing desired state.
2. Extract deploy planning result before mutation.
3. Emit structured dry-run plan: image/revision, target nodes, release task, ingress, secret refs, DNS findings.
4. Run tests.
5. Commit.

## P1 tranche

- Atomic solo state writes and basic cross-process locking.
- Idempotent already-desired semantics for detach/remove/secret delete/uninstall.
- Provider node-create retry safety.
- Structured logs/diagnose findings and optional support bundle/readiness report.
- Uninstall cleanup for env networks.
- Known limitations, install/upgrade, troubleshooting, config reference, release/rollback docs.

## Release/dogfood loop after each tranche

1. Push branch.
2. Trigger component release for preview/GA candidate with current SHA.
3. Download CLI+agent artifacts from GitHub release.
4. Install release CLI locally and release agent on dogfood nodes.
5. Force fresh sample app revision.
6. Verify deploy/status/URLs/logs/diagnostics/exec/rollback/cleanup.
7. Update readiness report with evidence and blockers.

## Stop conditions

Only call solo GA-ready when:

- P0 tasks are merged/released/dogfooded.
- Official release artifacts, not local-only builds, pass end-to-end solo dogfood.
- Status/diagnostics explain actual runtime reality.
- Rollback restores historical release semantics.
- Agents can dry-run before mutating.
- Known limitations are explicit and honest.
