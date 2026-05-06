---
title: CloudStack VMs
description: Use an Apache CloudStack instance as a devopsellence node.
---

devopsellence can run on a VM provisioned by Apache CloudStack. CloudStack
provides the IaaS layer: instance, network, public IP, firewall or security
group rules, and SSH access. devopsellence treats that instance as an ordinary
node.

Use this path when the CloudStack VM already exists or when an operator wants to
provision the VM through CloudStack, Terraform, cloudmonkey, or another existing
tool.

## VM requirements

Create an Ubuntu VM with:

- key-based SSH from the workstation or operator host;
- a user that can install and run Docker;
- outbound network access for image pulls and agent updates;
- inbound SSH from the operator host;
- inbound HTTP and HTTPS if the node will serve public ingress.

CloudStack network setups vary. Before registering the node, confirm that the
VM's public address, static NAT, firewall rules, port forwarding rules, or
security group rules expose the ports devopsellence needs.

## Solo mode

Register the CloudStack VM as a normal SSH node:

```bash
devopsellence node create prod-1 --host 203.0.113.10 --user root --ssh-key ~/.ssh/id_ed25519
devopsellence agent install prod-1
devopsellence node attach prod-1
devopsellence doctor
```

Then deploy as usual:

```bash
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
```

## Shared mode

For shared mode, register the existing CloudStack VM from the workspace:

```bash
devopsellence node register
```

Run the install command returned by the CLI on the CloudStack VM. The node agent
will register with the control plane, receive desired-state assignments, and
reconcile containers on the VM.

## Boundary

CloudStack should stay the infrastructure provider in this workflow. It creates
and exposes the VM. devopsellence installs the node agent, publishes desired
state, resolves secrets through configured adapters, pulls images, configures
Envoy, and reports status.

Do not use CloudStack user data as the desired-state store. User data is useful
for first-boot bootstrap, but desired state should remain the devopsellence node
agent contract.

## Troubleshooting

Check CloudStack first when the node is unreachable:

- the VM is running;
- the public IP or static NAT target is correct;
- SSH is open from the operator host;
- HTTP and HTTPS are open for ingress nodes;
- the selected network offering provides user data only if bootstrap depends on
  cloud-init.

Then inspect the node through devopsellence:

```bash
devopsellence doctor
devopsellence node diagnose prod-1
devopsellence node logs prod-1 --lines 100
```
