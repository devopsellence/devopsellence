---
name: devopsellence-rails-app
description: Use when building, modifying, testing, deploying, or scaling the blessed devopsellence Rails application baseline. Covers Rails 8.1, SQLite-first MVPs, Hotwire, Tailwind, Solid Queue/Cache/Cable, stack expansion, security checks, Docker, mise, and devopsellence solo on Linux servers.
---

# devopsellence Rails App

Use this skill inside apps generated from the devopsellence Rails template.

## Defaults

- Rails 8.1, Ruby 3.4+, SQLite, Puma, Thruster, Hotwire, Turbo, Stimulus, ERB, importmap, Propshaft, and Tailwind.
- Solid Queue, Solid Cache, and Solid Cable before Redis or Sidekiq.
- Keep the first MVP local and portable: SQLite, file-backed development defaults, no external service dependency unless the product need is explicit.
- Active Storage only when uploads are part of the product workflow. Use local storage first, then S3-compatible object storage when durability or multiple nodes require it.
- Built-in Rails authentication with `bcrypt`; use Pundit for authorization.
- ViewComponent, Pagy, lucide icons, Brakeman, bundler-audit, and rubocop-rails-omakase when they fit the app's actual UI, pagination, icon, security, or lint needs.
- Minitest, fixtures, Capybara system tests, and focused integration tests.
- `mise` owns language/tool versions. Do not replace it with ad hoc local setup docs.

## Do Not Add By Default

- Devise, Sidekiq, Redis, React, Next.js, Vite, GraphQL, Elasticsearch, Meilisearch, Kubernetes, or an admin framework.
- Extra gems, hosted services, or observability vendors before the app has a real product need.
- A second deployment system. Use devopsellence for deploy, secrets, logs, status, rollback, and node operations.

## Build Loop

1. Inspect the app and the user's request.
2. Keep changes idiomatic Rails and close to the behavior.
3. Add or update focused tests.
4. Run the narrowest useful command first, then broader checks before handoff:
   - `mise install`
   - `bin/rails db:prepare`
   - `bin/rails test`
   - `bin/rails test:system` when UI behavior changed
   - `bin/brakeman`
   - `bundle exec bundler-audit check --update`
5. Keep production concerns wired while building features: health checks, background jobs, uploads, secrets, logs, and deploy config.

## Stack Expansion

Start with SQLite and the smallest deployable Rails shape. When the MVP has real production pressure, add capabilities deliberately:

- Backups and restore drills: follow https://docs.devopsellence.com/guides/backup-restore/ before risky migrations, data imports, or production cutovers.
- PostgreSQL: move to managed or dedicated PostgreSQL when concurrency, data size, operations, reporting, extensions, or team practices outgrow SQLite.
- Durable uploads: move Active Storage to S3-compatible object storage when uploaded files must survive node replacement or be shared across nodes.
- Email: add a transactional email provider only when the product sends real user-facing mail.
- Monitoring: add Sentry and OpenTelemetry when production error reporting, traces, or alerting are needed.
- DNS/CDN: add Cloudflare DNS/CDN after the user confirms the zone, hostname, and mutation plan.

Keep every expansion visible in the implementation plan. Prefer explicit follow-up tasks over silently adding services during the first feature slice.

## Production Shape

- Start with one Rails web process, SQLite, Solid tables, JSON logs, and devopsellence deploy.
- Scale to medium-company shape by adding web nodes, splitting workers, moving PostgreSQL to managed or dedicated infrastructure, adding object storage, adding Sentry/OpenTelemetry, and separating Solid Queue/Cache/Cable databases or pools when pressure appears.
- Keep ordinary-tool escape hatches visible: SSH, Docker, logs, files, SQL, JSON, and cloud CLIs.

## devopsellence Loop

1. Configure `devopsellence.yml` with explicit services, ports, health checks, and worker processes.
2. Store production secrets with `devopsellence secret set --stdin`; never commit secret values.
3. Run `devopsellence deploy --dry-run` before production mutations.
4. After deploy, collect `devopsellence status`, app logs, node logs, and HTTPS evidence when ingress is configured.
