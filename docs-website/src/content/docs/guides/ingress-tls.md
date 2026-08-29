---
title: Ingress and TLS
description: Hostnames, Envoy, HTTPS, and DNS checks.
---

Public ingress is Envoy in both modes. Desired state carries hostnames, route
rules, TLS mode, and HTTP redirect behavior.

For solo HTTPS, point DNS at each web node, then configure ingress:

```bash
devopsellence ingress set --service web --host app.example.com --tls-email ops@example.com
# update DNS so app.example.com points at the attached web node IP before deploy
devopsellence deploy
devopsellence ingress check --wait 5m
devopsellence status
curl https://app.example.com/
```

Pass `--service` when the target web service is not obvious.

With auto TLS, `deploy` runs a DNS preflight and expects the hostname to resolve
to the attached web node IPs. `ingress check --wait` verifies DNS and TLS after
deploy publishes the ingress desired state and the agent has reconciled Envoy.
Use `devopsellence status` and `curl` as direct runtime proof before treating
HTTPS as ready.

## Auto TLS On Multiple Nodes

Auto TLS uses the same node-agent mechanism in solo and devopsellence: desired
state carries ingress intent plus node peers, and each web node runs an ACME
HTTP-01 challenge responder. If Let's Encrypt asks one node for a challenge token
that another node created, the node can fetch the challenge response from its web
peers and serve it back.

This matters most in solo mode because there is no hosted control plane in the
runtime path. The local CLI can still publish enough desired state over SSH for a
multi-node, local-only deployment to cooperate at runtime.

Multi-node `tls.mode: auto` requires:

- DNS for the hostname reaches the attached web nodes;
- port 80 is reachable for HTTP-01 validation;
- attached web nodes have public addresses in node state;
- the nodes can reach each other over HTTP for challenge forwarding.

Certificates are stored on each node rather than in a shared hosted certificate
store. Treat HTTPS as ready only after `devopsellence ingress check --wait`,
`devopsellence status`, and a direct HTTPS request succeed.

For local experiments without a real hostname, `sslip.io` can be useful when a
node has one public IP:

```bash
devopsellence ingress set --service web --host '203.0.113.10.sslip.io' --tls-mode auto
```
