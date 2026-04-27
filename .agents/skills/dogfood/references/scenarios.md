# Dogfood Scenarios

## solo-rails-first-deploy

Persona: solo Rails founder, infra-aware but impatient.

Goal: deploy a fresh Rails app with devopsellence solo.

Allowed blind-pass context: README, docs, CLI help, command output, product logs/status surfaced by commands.

Success:

- App reachable.
- Deploy status understandable.
- Secret path discoverable.
- Logs/status path discoverable.
- Delete or cleanup path clear.

Probe:

- Install/setup friction.
- First command discoverability.
- Error recovery.
- Time to first useful feedback.
- Confidence after deploy.

## solo-hetzner-provider-first-deploy

Persona: solo founder who wants devopsellence to create the VM.

Goal: deploy a fresh app with devopsellence solo using a Hetzner-created VM.

Allowed blind-pass context: README, docs, CLI help, command output, product logs/status surfaced by commands, and `op` for retrieving the Hetzner token.

Setup:

- Retrieve the API token from 1Password item `hetzner-devopsellence-solo` with `op`.
- Export the token as `HCLOUD_TOKEN` or pass it to `devopsellence provider login hetzner --token "$HCLOUD_TOKEN"`.
- Do not record the token value, raw `op` output, or raw item JSON in `commands.log` or reports.
- Create the node with `devopsellence node create prod-1 --provider hetzner --install --attach`.

Success:

- Provider login is discoverable and token errors are clear.
- Hetzner server creation returns usable node metadata.
- Agent install reaches a usable SSH/Docker/node-agent state.
- Deploy status and public URL are understandable.
- Cleanup deletes the provider VM and local node state after detach.

Probe:

- 1Password token retrieval friction.
- Default Hetzner region/size clarity.
- SSH key generation/reuse messaging.
- Provider API error quality.
- Cleanup confidence and cost-leak risk.

## existing-app-secrets-redeploy

Persona: Rails developer adding production-like config.

Goal: deploy an existing app, add a secret, redeploy, verify status.

Success:

- Secret command or workflow is discoverable.
- Secret value does not leak in output/report.
- Redeploy makes state understandable.
- Failed secret usage has clear recovery.

Probe:

- Naming of app/environment/secret scopes.
- Whether local and remote state are easy to distinguish.
- Whether status explains which revision/config is active.

## failed-deploy-recovery

Persona: tired maintainer at night.

Goal: diagnose and recover from a broken deploy.

Failure seeds:

- Bad image or build command.
- Missing secret.
- Bad port.
- Unreachable node.
- App starts then exits.

Success:

- Failure is surfaced without source inspection.
- Next step is obvious.
- Logs are reachable.
- Retrying after fix is boring.

Probe:

- Error specificity.
- Whether failed desired state is visible.
- Whether rollback/delete/cleanup is understandable.

## shared-node-connect-deploy

Persona: user evaluating hosted/shared control plane.

Goal: connect a node, deploy app, inspect status.

Success:

- Node enrollment is understandable.
- Hosted vs local responsibilities are clear.
- Status reflects node/app state.
- Escape hatches remain ordinary: SSH, Docker, logs, JSON.

Probe:

- Account/environment naming.
- Agent reconciliation mental model.
- Trust boundary clarity.
