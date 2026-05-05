# solo production operations

Solo mode is for a single operator or agent working from a trusted workstation
or admin box against directly owned VMs. It should be structured, repeatable,
and recoverable, but it is not the CI/CD product surface. Use shared mode for
CI deploys, team credentials, deploy locks, provenance, and audit trails.

This guide is the production golden path for solo until the public docs carry a
full runbook.

## preflight

Run local and remote diagnostics before the first production deploy, after node
changes, and before high-risk rollouts.

```sh
devopsellence doctor
devopsellence node diagnose <node>
```

`doctor` is the release gate: config, local state, attached nodes, runtime
status, agent version, and node security findings summarized as pass/fail
checks.

`node diagnose` is the node evidence command: SSH reachability, agent status,
agent version, desired-state files, Docker containers, listening ports, recent
logs, and node security findings.

Security hardening belongs here as baseline drift detection, not as a separate
VM security platform. devopsellence should flag production-relevant risks such
as password SSH, weak state or TLS key permissions, Docker socket mounts,
privileged containers, and unexpected public listeners. It should leave deeper
host policy, patch management, intrusion detection, and compliance ownership to
ordinary infrastructure tools.

## deploy

Always inspect the dry-run before applying production changes.

```sh
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
```

Read `rollout_contract` in the dry-run or final result:

- web services use health-gated cutover;
- non-web services stop the old container before starting the new one;
- release tasks run as one-shot tasks and may change data before app rollout.

After deploy, `status` is the source of truth for the current release,
deployment, desired-state revision, observed runtime revision, health, public
URLs, and any recovered interrupted deployment.

## agent upkeep

Keep the node agent on the expected version.

```sh
devopsellence agent install <node>
devopsellence agent upgrade <node>
devopsellence doctor
```

`agent install` verifies the installed version and active service state.
`doctor` fails when attached nodes are running an unexpected agent version.

## rollback

Use rollback as a desired-state republish, not as an automatic data rewind.

```sh
devopsellence release list
devopsellence release rollback --dry-run <release-id-or-revision-prefix>
devopsellence release rollback <release-id-or-revision-prefix>
```

Read `rollback_contract` before applying:

- data rollback is not automatic;
- the selected release task may rerun;
- the operator must verify schema and data compatibility;
- a backup or restore point should exist before risky migrations.

Backups should stay aligned with devopsellence's north star: ordinary tools,
explicit restore drills, and app-owned data services running on familiar VMs.
devopsellence can make backups visible in plans and diagnostics over time, but
rollback should not pretend to recover data it did not back up and restore.

## diagnosis

Use structured commands before falling back to ad-hoc SSH.

```sh
devopsellence status
devopsellence doctor
devopsellence node diagnose <node>
devopsellence logs <service>
devopsellence node logs <node>
devopsellence support bundle
```

These commands should preserve the agent-primary contract: machine-readable
evidence, stable failures, redacted secrets, and next safe actions. SSH, Docker,
files, and logs remain valid escape hatches when the structured surface is not
enough.
