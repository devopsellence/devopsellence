---
title: Ingress and TLS
description: Hostnames, Envoy, HTTPS, and DNS checks.
---

Public ingress is Envoy in both modes. Desired state carries hostnames, route
rules, TLS mode, and HTTP redirect behavior.

For solo HTTPS, point DNS at each web node, then configure ingress:

```bash
devopsellence ingress set --service web --host app.example.com --tls-email ops@example.com
devopsellence ingress check --wait 5m
devopsellence deploy
devopsellence status
curl https://app.example.com/
```

Pass `--service` when the target web service is not obvious.

`ingress check --wait` checks DNS readiness. After deploy, use
`devopsellence status` and `curl` to confirm TLS reachability.

For local experiments without a real hostname, `sslip.io` can be useful when a
node has one public IP:

```bash
devopsellence ingress set --host '203.0.113.10.sslip.io' --tls-mode auto
```
