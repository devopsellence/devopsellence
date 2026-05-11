---
name: devopsellence-index-php-app
description: Use when building, modifying, testing, deploying, or scaling the devopsellence index.php application baseline. Covers PHP 8.4, one-file-first apps, PDO SQLite, optional jQuery, Docker, mise, backups, and devopsellence solo on Linux servers.
---

# devopsellence index.php App

Use this skill inside apps generated from the devopsellence index.php template.

## Defaults

- PHP 8.4, nginx latest with PHP-FPM, one `public/index.php` entrypoint, PDO SQLite, no framework, no build step, Docker, and mise.
- SQLite lives in `data/app.sqlite` locally and `/app/data/app.sqlite` in production.
- Enable SQLite WAL and `busy_timeout` for the default single-node production shape.
- Use plain HTML, CSS, and JavaScript first. Add jQuery only when it keeps the code smaller and clearer.
- Keep the first MVP local and portable: one writable node, one persistent volume, no external service dependency unless the product need is explicit.
- Store secrets with devopsellence. Never commit secret values.

## Do Not Add By Default

- Laravel, Symfony, Slim, Composer packages, React, Next.js, Vite, TypeScript, Redis, queues, Postgres, or an admin framework.
- Extra hosted services, observability vendors, or managed databases before the app has real production pressure.
- A second deployment system. Use devopsellence for deploy, secrets, logs, status, rollback, and node operations.

## Build Loop

1. Inspect the app and the user's request.
2. Start with the current `public/index.php`; split files only when the product earns it.
3. Keep data access through PDO prepared statements.
4. Run the narrowest useful command first, then broader checks before handoff:
   - `mise install`
   - `scripts/check`
   - `php -S 127.0.0.1:8000 -t public` for manual local smoke checks
   - `docker build .` when deploy packaging changed
5. Keep production concerns wired while building features: health checks, persistent SQLite volume, backups, secrets, logs, and deploy config.

## Stack Expansion

Start with one PHP web process and SQLite. Add capabilities deliberately when the MVP has real pressure:

- Backups and restore drills: use `scripts/backup-sqlite` locally and follow https://docs.devopsellence.com/guides/backup-restore/ before risky migrations, data imports, or production cutovers.
- PostgreSQL: move to managed or dedicated PostgreSQL when write concurrency, reporting, data size, operations, team workflows, or multi-node writes outgrow SQLite.
- Durable uploads: move uploaded files to S3-compatible object storage when files must survive node replacement or be shared across nodes.
- Email: add a transactional email provider only when the product sends real user-facing mail.
- Monitoring: add Sentry/OpenTelemetry only when production error reporting, traces, or alerting are needed.
- DNS/CDN: add Cloudflare DNS/CDN after the user confirms the zone, hostname, and mutation plan.

Keep every expansion visible in the implementation plan. Prefer explicit follow-up tasks over silently adding services during the first feature slice.

## Production Shape

- Start with one nginx/PHP-FPM container, one SQLite database on a devopsellence volume, JSON-compatible logs, `/healthz`, and devopsellence deploy.
- Keep one writable web node while SQLite is the production database. Do not scale multiple writers against a shared SQLite file.
- Move to a client/server database before multi-node writes, high write concurrency, or team-operated production workflows.
- Keep ordinary-tool escape hatches visible: SSH, Docker, logs, files, SQL, JSON, and cloud CLIs.

## devopsellence Loop

1. Keep `devopsellence.yml` explicit: web port, `/healthz`, `/app/data` volume, and runtime env.
2. Store production secrets with `devopsellence secret set --stdin`; never commit secret values.
3. Run `devopsellence deploy --dry-run` before production mutations.
4. After deploy, collect `devopsellence status`, app logs, node logs, and HTTPS evidence when ingress is configured.
