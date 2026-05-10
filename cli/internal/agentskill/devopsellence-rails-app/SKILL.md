---
name: devopsellence-rails-app
description: Use when building, modifying, testing, deploying, or scaling the blessed devopsellence Rails application baseline. Covers Rails 8.1, PostgreSQL, Hotwire, Tailwind, Solid Queue/Cache/Cable, Active Storage, Sentry, OpenTelemetry, security checks, Docker, mise, and devopsellence solo on Linux servers.
---

# devopsellence Rails App

Use this skill inside apps generated from the devopsellence Rails template.

## Defaults

- Rails 8.1, Ruby 3.4+, PostgreSQL, Puma, Thruster, Hotwire, Turbo, Stimulus, ERB, importmap, Propshaft, and Tailwind.
- Solid Queue, Solid Cache, and Solid Cable before Redis or Sidekiq.
- Active Storage with S3-compatible object storage for durable uploads.
- Built-in Rails authentication with `bcrypt`; use Pundit for authorization.
- ViewComponent, Pagy, lucide icons, Sentry, OpenTelemetry, Brakeman, bundler-audit, and rubocop-rails-omakase.
- Minitest, fixtures, Capybara system tests, and focused integration tests.
- `mise` owns language/tool versions. Do not replace it with ad hoc local setup docs.

## Do Not Add By Default

- Devise, Sidekiq, Redis, React, Next.js, Vite, GraphQL, Elasticsearch, Meilisearch, Kubernetes, or an admin framework.
- Extra gems before the app has a real product need.
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

## Production Shape

- Start with one Rails web process, PostgreSQL, Solid tables, object storage, Sentry, OpenTelemetry, JSON logs, and devopsellence deploy.
- Scale to medium-company shape by adding web nodes, splitting workers, moving PostgreSQL to managed or dedicated infrastructure, and separating Solid Queue/Cache/Cable databases or pools when pressure appears.
- Keep ordinary-tool escape hatches visible: SSH, Docker, logs, files, SQL, JSON, and cloud CLIs.

## devopsellence Loop

1. Configure `devopsellence.yml` with explicit services, ports, health checks, and worker processes.
2. Store production secrets with `devopsellence secret set --stdin`; never commit secret values.
3. Run `devopsellence deploy --dry-run` before production mutations.
4. After deploy, collect `devopsellence status`, app logs, node logs, and HTTPS evidence when ingress is configured.
