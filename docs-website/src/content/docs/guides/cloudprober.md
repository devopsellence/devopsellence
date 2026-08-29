---
title: Cloudprober
description: Run synthetic probes as an ordinary devopsellence service.
---

[Cloudprober](https://cloudprober.org/) is active monitoring software. It runs
HTTP, TCP, DNS, ping, gRPC, UDP, browser, and external probes, then exposes
status, metrics, and alerts.

Use it when you want a small service that checks your application from the
same VM, another devopsellence node, or a dedicated monitoring node. It is not
a devopsellence subsystem. devopsellence starts and reconciles the container;
Cloudprober owns probe config, metrics, and alert delivery.

## Create a probe config

Create `cloudprober.cfg` locally. This example checks an HTTPS app endpoint
every 10 seconds and treats any 2xx response as healthy:

```bash
cat > cloudprober.cfg <<'EOF'
probe {
  name: "app_https"
  type: HTTP
  targets {
    host_names: "app.example.com"
  }
  http_probe {
    protocol: HTTPS
    relative_url: "/up"
  }
  validator {
    http_validator {
      success_status_codes: "200-299"
    }
  }
  interval: "10s"
  timeout: "2s"
}
EOF
```

Copy it to the node that will run Cloudprober:

```bash
chmod 0600 cloudprober.cfg
ssh <user>@<host> 'sudo sh -c "mkdir -p /etc/devopsellence && tmp=\$(mktemp /etc/devopsellence/cloudprober.cfg.XXXXXX) && chmod 0600 \"\$tmp\" && cat > \"\$tmp\" && mv \"\$tmp\" /etc/devopsellence/cloudprober.cfg"' < cloudprober.cfg
```

Use the SSH user and host from your node record. The `sudo` step is needed when
the SSH user cannot write under `/etc` directly. This flow avoids a readable
temporary copy under `/tmp` and does not change permissions on an existing
`/etc/devopsellence` directory. Keep the source config private; Cloudprober
configs may grow alerting credentials over time. If your selected image runs as
a non-root user, adjust the owner or group so the container can read the file
without making it world-readable. The node copy is a runtime input, not hidden
devopsellence state.

## Add the service

Add Cloudprober as a normal supporting service in `devopsellence.yml`:

```yaml
services:
  web:
    ports:
      - name: http
        port: 3000
    healthcheck:
      path: /up
      port: 3000

  cloudprober:
    image: ghcr.io/cloudprober/cloudprober:v0.14.3
    ports:
      - name: http
        port: 9313
    healthcheck:
      path: /status
      port: 9313
    volumes:
      - source: /etc/devopsellence/cloudprober.cfg
        target: /etc/cloudprober.cfg
```

Cloudprober exposes a status UI at `/status`, the active configuration at
`/config`, active alerts at `/alerts`, and Prometheus-format metrics at
`/metrics`.

Run a normal deploy:

```bash
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
```

This example includes an `http` port and health check, so devopsellence treats
Cloudprober as a `web` service and can place it on the default `web` nodes. To
run Cloudprober as a private worker-style service instead, omit `ports` and
`healthcheck`, then place it on a node with a `worker` label. Without a
healthcheck, devopsellence can still keep the container running, but it cannot
report Cloudprober service health.

## Inspect results

In solo mode, use `devopsellence exec` when you want to inspect Cloudprober from
inside the service:

```bash
devopsellence exec cloudprober -- wget -qO- http://127.0.0.1:9313/status
devopsellence exec cloudprober -- wget -qO- http://127.0.0.1:9313/metrics
```

In devopsellence company workflows today, inspect the node with direct SSH or
your existing monitoring collector until managed exec tunnel support is
available.

Expose Cloudprober publicly only when you have a reason to do so and the
endpoint is acceptable for your environment. For most teams, keep it private.
A collector running as another service in the same environment can scrape
`http://cloudprober:9313/metrics` over the environment Docker network. A
host-level collector needs an explicit exposure path, such as ingress or
separate host-port wiring outside devopsellence.

## Alerts and exporters

Cloudprober can notify Slack, PagerDuty, Opsgenie, email, or an HTTP webhook
from its own config. It can also export metrics to Prometheus, OpenTelemetry,
CloudWatch, Google Cloud Monitoring, PostgreSQL, Pub/Sub, Datadog, and BigQuery.

That integration belongs in the Cloudprober config and the destination system,
not in devopsellence. devopsellence should keep the service running and make it
easy to inspect through ordinary commands.

## Boundary

Use devopsellence for the foundation:

- place Cloudprober on a VM;
- pull and run the container;
- mount the config file;
- restart it when desired state changes;
- show whether the service is healthy when a healthcheck is configured.

Use Cloudprober for monitoring behavior:

- probe definitions;
- success validators;
- alert rules and notifications;
- metrics export;
- dashboard integration.
