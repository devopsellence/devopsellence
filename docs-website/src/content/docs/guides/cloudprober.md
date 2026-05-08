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
devopsellence node exec prod-1 -- install -d /etc/devopsellence
scp cloudprober.cfg root@203.0.113.10:/etc/devopsellence/cloudprober.cfg
```

Use the SSH user and host from your node record. Keep the source config in your
own infrastructure notes or configuration management. The node copy is a runtime
input, not hidden devopsellence state.

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

If the node only has the default `web` label, add `worker` before deploying
supporting services:

```bash
devopsellence node label set prod-1 --labels web,worker
devopsellence deploy
```

## Inspect results

Use service exec when you want to inspect Cloudprober from inside the service:

```bash
devopsellence exec cloudprober -- wget -qO- http://127.0.0.1:9313/status
devopsellence exec cloudprober -- wget -qO- http://127.0.0.1:9313/metrics
```

Expose Cloudprober publicly only when you have a reason to do so and the
endpoint is acceptable for your environment. For most teams, keep it private
and let Prometheus, Grafana Agent, Datadog Agent, or another collector scrape it
from the node network.

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
- show whether the service is healthy.

Use Cloudprober for monitoring behavior:

- probe definitions;
- success validators;
- alert rules and notifications;
- metrics export;
- dashboard integration.
