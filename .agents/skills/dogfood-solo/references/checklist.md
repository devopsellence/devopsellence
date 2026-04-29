# Solo Dogfood Checklist

Use this checklist as a menu, not a bureaucracy. For PR-focused probes, run only the affected sections plus any adjacent high-risk surfaces. For RC/release validation, run the full checklist with official artifacts.

## 0. Preflight

- [ ] Create run artifact with `mise exec -- ruby .agents/skills/dogfood-solo/scripts/new_run.rb <scenario>` when Ruby is managed by mise; use plain `ruby ...` if Ruby is already on `PATH`.
- [ ] Record branch, SHA, PR, target version, and validation mode.
- [ ] Confirm whether this is local-build validation or official-artifact validation.
- [ ] Record node strategy: existing approved node, zirk VM, provider VM, or user-provided SSH host.
- [ ] Record cleanup plan before creating/deleting resources.
- [ ] Confirm no secrets will be printed or persisted in reports.

Useful commands:

```sh
git status --short --branch
git rev-parse HEAD
gh pr view <number> --json number,url,headRefOid,reviewDecision,statusCheckRollup || true
devopsellence --version || true
devopsellence mode show || true
devopsellence context show || true
```

## 1. Official Artifact Reality

Required for RC/release validation.

- [ ] Download/install official CLI artifact for the exact target version/commit.
- [ ] Download/install official node-agent artifact on every dogfood node.
- [ ] Verify CLI version/commit.
- [ ] Verify node-agent version/commit.
- [ ] Force a fresh workload revision after artifact install.

Do not treat `cli/scripts/release-local.sh` as proof of node-agent behavior; it builds/installs the CLI only.

## 2. Solo Deploy Baseline

- [ ] Initialize or verify solo mode.
- [ ] Attach/create/install a node.
- [ ] Run `doctor` before deploy.
- [ ] Deploy a fresh revision.
- [ ] Verify CLI-reported status and runtime endpoint health.
- [ ] Verify logs and exec for the deployed service.

Useful commands:

```sh
devopsellence init --mode solo
devopsellence doctor
devopsellence deploy
devopsellence status
devopsellence logs --node <node> --lines 100
devopsellence exec <service> -- <command>
curl -fsS <url>/up
```

## 3. TLS Auto / ACME

- [ ] Use a real hostname resolving to the node; `sslip.io` hostnames are acceptable.
- [ ] Configure `tls.mode: auto`, TLS email, and `redirect_http` when testing redirect.
- [ ] Run ingress check and record DNS result.
- [ ] Deploy and wait for settled status.
- [ ] Verify HTTPS with `curl`.
- [ ] Verify HTTP redirects to HTTPS when enabled.
- [ ] Scan node-agent logs from the relevant time for ACME success/failure.
- [ ] Restart devopsellence node agent and verify cert reuse/endpoints.
- [ ] Reboot node and verify endpoints recover.

Evidence markers:

```text
acme certificate ready
curl https://<host>/up -> success
curl -I http://<host>/... -> 301/308 Location: https://...
```

## 4. Manual TLS Honesty

- [ ] Check whether the CLI/docs expose a cert/key provisioning path.
- [ ] If no cert/key install path exists, mark as known product gap; do not claim dogfood success.
- [ ] If a path exists, install cert/key material without logging secrets/private key data.
- [ ] Deploy and verify `curl https://<host>/up` succeeds.
- [ ] Verify missing/invalid cert material produces clear status/warnings.

Known gap from 2026-04 dogfood: `tls.mode: manual` was exposed, but no clear solo cert/key provisioning path was found. See issue #95.

## 5. Multi-Project Co-hosting

- [ ] Deploy project A to node with hostname A.
- [ ] Deploy project B to same node with hostname B.
- [ ] Verify both HTTPS endpoints return the expected app.
- [ ] Verify `status` shows settled state without route/env collision.
- [ ] Verify `logs` and `exec` target the right project's containers.
- [ ] Verify `release list` and rollback for at least one co-hosted project.
- [ ] Detach/remove one project and verify the other remains healthy.
- [ ] Reattach/redeploy removed project and verify all endpoints recover.

Failure classes to watch:

- duplicate route validation
- ingress TLS/redirect merge mismatch
- raw `production` runtime-name collisions
- logs/exec selecting no or wrong containers
- stale rollout status accepted after reattach/redeploy

## 6. Multi-Environment on One Node

- [ ] Configure production and staging for one project with distinct hostnames.
- [ ] Attach both environments to the same node.
- [ ] Set environment-specific secrets.
- [ ] Deploy production and staging.
- [ ] Verify each host routes to the expected environment.
- [ ] Verify `DEVOPSELLENCE_ENVIRONMENT=staging` or current context is honored by status/logs/exec/secrets/release list/rollback.
- [ ] Roll back staging and verify production is unaffected.

Failure classes to watch:

- commands defaulting to production when staging is selected
- duplicate route from unresolved/default hostname
- secrets stored/listed/deleted in wrong environment
- release list or rollback using default config instead of current env

## 7. Secret Isolation

- [ ] Use the same secret name across production, staging, and a second project.
- [ ] Set secrets via stdin; never put values directly in shell history.
- [ ] Deploy after secret changes.
- [ ] Verify inside each environment that the value is the expected redacted category, without printing raw values to the report.
- [ ] Re-check earlier environments after deploying another project.

Useful pattern:

```sh
printf '%s' "$VALUE" | devopsellence secret set DOGFOOD_SECRET --service web --stdin --env staging
devopsellence secret list --env staging
devopsellence deploy
devopsellence exec web -- printenv DOGFOOD_SECRET
```

Report values as `[REDACTED: app production]`, not plaintext.

## 8. Release List and Rollback

- [ ] Create at least two releases in the target environment.
- [ ] Confirm `release list` reports the selected logical environment.
- [ ] Roll back one environment while another environment/project is co-hosted.
- [ ] Verify current release changed only after successful publication.
- [ ] Verify endpoints stay healthy.
- [ ] If inducing failure, verify previous current release remains current.

Watch for rollback rebuilding from current `devopsellence.yml` instead of the stored historical snapshot.

## 9. Dry-Run / Plan Boundary

- [ ] Run deploy dry-run and rollback dry-run where available.
- [ ] Verify no build, publish, SSH, release mutation, deployment mutation, or state write occurred.
- [ ] Compare release list/state before and after.

Evidence marker from prior run:

```text
build: false
publish: false
ssh: false
state_write: false
```

## 10. DNS Honesty

- [ ] Run ingress check for a valid hostname.
- [ ] Run ingress check for an intentionally bad hostname such as `example.com` when safe.
- [ ] Verify output shows resolved IPs and missing expected node IP.
- [ ] Ensure deploy/status does not imply ready HTTPS when DNS/TLS is not ready.

Evidence shape:

```json
{
  "ok": false,
  "missing": ["<node-ip>"]
}
```

## 11. Restart / Reboot Recovery

- [ ] Capture status and endpoint health before restart.
- [ ] Restart `devopsellence-agent` or equivalent node-agent service.
- [ ] Verify all co-hosted endpoints recover.
- [ ] Trigger full node reboot only with user approval.
- [ ] Poll node reachability/status after reboot.
- [ ] Verify every expected endpoint and selected secrets/releases after reboot.

A reboot command timing out is inconclusive; the proof is post-reboot status and endpoint health.

## 12. Diagnostics and Agent-Primary Output

- [ ] Check whether bounded commands produce one machine-readable result where expected.
- [ ] Check whether long commands produce parseable event streams.
- [ ] Check structured error shape: code, message, exit code, suggested next action/evidence fields.
- [ ] Avoid making decisions from prose-only spinners/styled text when structured output should exist.

Commands to probe:

```sh
devopsellence status
devopsellence logs --node <node> --lines 100
devopsellence node logs <node> --lines 100
devopsellence node diagnose <node>
devopsellence release list --limit 5
devopsellence exec <service> -- <command>
```

## 13. Report and Ratchet

- [ ] Summarize verdict and confidence.
- [ ] Link evidence files.
- [ ] Convert every material finding into a regression test, issue, docs update, skill update, or follow-up PR.
- [ ] Confirm cleanup state.
- [ ] If PR-related, update PR body/comment with tests and dogfood evidence.
