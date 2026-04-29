# devopsellence Solo Dogfood Report

Scenario:
Target version/commit:
Validation mode:
Date:
Commit:
Run path:
Node(s):
Hostnames:
PR/issue links:

## Verdict

- Verdict: <ready | ready with known gaps | blocked | inconclusive>
- One-line reason:

## Executive Summary

<What was validated, what failed, what changed, and what remains.>

## Evidence Index

- Commands log: `commands.log`
- Versions/artifacts:
- Deploy/status evidence:
- Endpoint checks:
- Logs/diagnostics:
- Issues/PRs:

## Probe Matrix

| Probe | Result | Evidence | Notes |
| --- | --- | --- | --- |
| Official CLI artifact/version | PASS/FAIL/SKIPPED |  |  |
| Official node-agent artifact/version | PASS/FAIL/SKIPPED |  |  |
| Solo deploy fresh revision | PASS/FAIL/SKIPPED |  |  |
| TLS auto / ACME | PASS/FAIL/SKIPPED |  |  |
| HTTP -> HTTPS redirect | PASS/FAIL/SKIPPED |  |  |
| Manual TLS operability | PASS/FAIL/SKIPPED/KNOWN GAP |  |  |
| Multi-project co-hosting | PASS/FAIL/SKIPPED |  |  |
| Multi-env co-hosting | PASS/FAIL/SKIPPED |  |  |
| Secret isolation | PASS/FAIL/SKIPPED |  |  |
| status/logs/exec diagnostics | PASS/FAIL/SKIPPED |  |  |
| release list / rollback | PASS/FAIL/SKIPPED |  |  |
| Dry-run side-effect boundary | PASS/FAIL/SKIPPED |  |  |
| DNS honesty | PASS/FAIL/SKIPPED |  |  |
| Detach/reattach | PASS/FAIL/SKIPPED |  |  |
| Agent restart recovery | PASS/FAIL/SKIPPED |  |  |
| Full node reboot recovery | PASS/FAIL/SKIPPED |  |  |
| Cleanup verified | PASS/FAIL/SKIPPED |  |  |

## Findings

### 1. <title>

- Severity: <blocker | high | medium | low>
- Type: <bug | product gap | docs gap | test gap | skill/process gap>
- Surface: <CLI | node agent | deploy core | state | docs | installer | diagnostics | cleanup>
- Expected:
- Actual:
- Reproduction/evidence:
- Suggested fix:
- Ratchet artifact: <test | issue | docs | skill update | PR>

## Fixes Made During Run

- <commit/test/PR summary or none>

## Tests Run

```text
<commands and outcomes>
```

## Live Runtime Verification

```text
<endpoint/status/log summaries, redacted>
```

## Skipped / Not Probed

- <probe>: <why skipped and whether it matters>

## Cleanup

- Resources created:
- Cleanup commands:
- Cleanup verification:
- Remaining resources/risk:

## Release Decision Notes

<Whether this should block merge/release, what can be follow-up, and why.>
