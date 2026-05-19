---
title: Run Sessy for Amazon SES
description: Use Amazon SES for sending and Sessy for observability on a devopsellence solo VM.
---

[Amazon SES](https://aws.amazon.com/ses/) is a fantastic email service when you want low-cost,
high-deliverability transactional mail. [Sessy](https://github.com/marckohlbrugge/sessy) fills the
operational gap: it receives SES event notifications and gives you a dashboard for sends, deliveries,
bounces, complaints, opens, clicks, and rendering failures.

This guide runs Sessy on a VM with `devopsellence solo`, then points SES event webhooks at it. Your
application still sends email through SES. Sessy observes what happens after the send.

## Prerequisites

- a VM reachable over SSH with Docker installed, or a provider-backed solo node created by devopsellence
- a DNS name such as `sessy.example.com` pointing at the VM
- the devopsellence CLI installed locally
- a local clone of Sessy
- an AWS account with SES access in the region you plan to use

```bash
git clone https://github.com/marckohlbrugge/sessy.git
cd sessy
devopsellence init --mode solo
```

## Add devopsellence config

Sessy's Dockerfile exposes port `80`, writes app data to `/rails/storage`, and serves the Rails health
check at `/up`. Keep Solid Queue inside Puma for the simple single-VM deployment.

Replace `sessy.example.com` and `ops@example.com` with your values.

```yaml
schema_version: 1
organization: solo
project: sessy
default_environment: production

build:
  context: .
  dockerfile: Dockerfile
  platforms:
    - linux/amd64

services:
  web:
    ports:
      - name: http
        port: 80
    healthcheck:
      path: /up
      port: 80
    volumes:
      - source: sessy_storage
        target: /rails/storage
    env:
      RAILS_ENV: production
      SOLID_QUEUE_IN_PUMA: "true"
    secret_refs:
      - name: SECRET_KEY_BASE
        secret: SECRET_KEY_BASE
      - name: HTTP_AUTH_USERNAME
        secret: HTTP_AUTH_USERNAME
      - name: HTTP_AUTH_PASSWORD
        secret: HTTP_AUTH_PASSWORD

tasks:
  release:
    service: web
    command:
      - ./bin/rails
      - db:prepare

ingress:
  hosts:
    - sessy.example.com
  rules:
    - match:
        host: sessy.example.com
        path_prefix: /
      target:
        service: web
        port: http
  tls:
    mode: auto
    email: ops@example.com
  redirect_http: true
```

Why this shape works:

- devopsellence terminates public TLS at ingress, so leave Sessy's production SSL defaults enabled.
- `/rails/storage` persists SQLite, Solid Queue, Solid Cache, Solid Cable, and local storage data.
- `SOLID_QUEUE_IN_PUMA=true` runs Sessy's recurring cleanup jobs without a second service.
- HTTP Basic Auth protects the dashboard. Sessy's SES webhook endpoints remain reachable without auth.

## Set secrets

Generate a Rails secret and set dashboard credentials. Prefer `--stdin` so secret values do not land in
shell history.

```bash
openssl rand -hex 64 | devopsellence secret set SECRET_KEY_BASE --service web --stdin
printf '%s' 'admin' | devopsellence secret set HTTP_AUTH_USERNAME --service web --stdin
printf '%s' '<strong-password>' | devopsellence secret set HTTP_AUTH_PASSWORD --service web --stdin
```

For production, you can store the same values in 1Password and reference them from solo secrets:

```bash
devopsellence secret set SECRET_KEY_BASE --service web --store 1password --op-ref "op://deploy/sessy/secret-key-base"
devopsellence secret set HTTP_AUTH_USERNAME --service web --store 1password --op-ref "op://deploy/sessy/http-auth-username"
devopsellence secret set HTTP_AUTH_PASSWORD --service web --store 1password --op-ref "op://deploy/sessy/http-auth-password"
```

## Deploy Sessy

If `prod-1` already exists, is reachable over SSH, has the agent installed, and is attached to this
workspace, deploy from the Sessy checkout:

```bash
devopsellence node attach prod-1
devopsellence doctor
devopsellence deploy --dry-run
devopsellence deploy
devopsellence status
```

If you have not created the node yet, follow step 3 in the
[Solo quickstart](/getting-started/solo-quickstart/) first, then return to this deploy command block.

Open `https://sessy.example.com`, sign in, and create a source for each SES configuration set you want
to observe. Sessy shows the webhook URL for that source, in this form:

```text
https://sessy.example.com/webhooks/<source-token>
```

## Configure Amazon SES

Set up SES in the same region your app will use to send mail.

1. Request production access if the account is still in the SES sandbox.
2. Verify the sending domain or email identity.
3. Enable DKIM for the identity, and add SPF, DMARC, and a custom MAIL FROM domain for better
   authentication and deliverability.
4. Create a configuration set such as `myapp-transactional`.
5. Create an SNS topic and subscribe the Sessy source webhook URL with HTTPS.
6. Add an SES configuration set event destination that sends events to the SNS topic.

Sessy's own [AWS SES setup guide](https://github.com/marckohlbrugge/sessy/blob/main/docs/aws-ses-setup.md)
has the detailed AWS Console and CLI flow. AWS also documents
[production access](https://docs.aws.amazon.com/ses/latest/dg/request-production-access.html),
[email authentication](https://docs.aws.amazon.com/ses/latest/dg/email-authentication-methods.html),
[SMTP credentials](https://docs.aws.amazon.com/ses/latest/dg/smtp-credentials.html), and
[regional SMTP endpoints](https://docs.aws.amazon.com/general/latest/gr/ses.html#smtp-endpoints).

## Send through SES with the configuration set

Your app sends mail through the SES SMTP endpoint for its region, usually on port `587` with STARTTLS.
SES SMTP credentials are region-specific and are not the same as normal AWS access keys.

Use the Sessy source's configuration set name on every email. For a Rails app, set the header globally
or per message:

```ruby
class ApplicationMailer < ActionMailer::Base
  default "X-SES-CONFIGURATION-SET" => "myapp-transactional"
end
```

Then configure your app's SMTP settings with values like:

```text
SMTP_ADDRESS=email-smtp.us-east-1.amazonaws.com
SMTP_PORT=587
SMTP_USERNAME=<ses-smtp-username>
SMTP_PASSWORD=<ses-smtp-password>
MAILER_FROM_ADDRESS=notifications@example.com
```

## Verify the loop

Send a real test email from your app, then check the Sessy source activity.

Useful checks:

```bash
devopsellence logs web --node prod-1 --lines 100
aws sns list-subscriptions-by-topic --topic-arn "$TOPIC_ARN" --region "$REGION"
aws ses describe-configuration-set \
  --configuration-set-name "$CONFIG_SET" \
  --configuration-set-attribute-names eventDestinations \
  --region "$REGION"
```

If Sessy receives no events, confirm the SNS subscription is confirmed, the SES event destination is
enabled, the webhook URL matches the Sessy source token, and the app is sending with the
`X-SES-CONFIGURATION-SET` header.
