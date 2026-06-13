---
title: Backup and restore
description: Run restic-backed backups as an ordinary service.
---

devopsellence does not need a first-class backup subsystem for solo mode. Use
the existing primitives: an ordinary service, service-scoped secrets, deploy,
logs, exec, and support bundles.

The backup repository stays yours. The database, object store, cache, queue,
and retention policy stay yours too. devopsellence should make the pattern easy
to run and inspect, not become a database or storage platform.

## Recommended Shape

Run restic from a service next to the app. The service can back up explicit
file paths, run database dumps, push encrypted snapshots to a repository, and
emit machine-readable evidence.

```yaml
services:
  web:
    ports:
      - name: http
        port: 3000
    healthcheck:
      path: /up
      port: 3000
    volumes:
      - source: app_storage
        target: /app/storage
    secret_refs:
      - name: DATABASE_URL
        secret: DATABASE_URL

  backup:
    image: registry.example.com/example-app-backup:<version>
    command:
      - sh
      - -lc
      - sleep infinity
    volumes:
      - source: app_storage
        target: /data/app_storage
    env:
      BACKUP_PATHS: /data/app_storage
      RESTIC_REPOSITORY: s3:s3.amazonaws.com/example-app-backups
      RESTIC_CACHE_DIR: /tmp/restic-cache
    secret_refs:
      - name: RESTIC_PASSWORD
        secret: RESTIC_PASSWORD
      - name: AWS_ACCESS_KEY_ID
        secret: AWS_ACCESS_KEY_ID
      - name: AWS_SECRET_ACCESS_KEY
        secret: AWS_SECRET_ACCESS_KEY
      - name: DATABASE_URL
        secret: DATABASE_URL
```

This is intentionally just another service. There is no `kind: backup`, hidden
scheduler, devopsellence-owned repository, or backup-specific runtime model.

## Secrets

Store repository and database credentials as service-scoped secrets.

```bash
devopsellence secret set RESTIC_PASSWORD --service backup --stdin
devopsellence secret set AWS_ACCESS_KEY_ID --service backup --stdin
devopsellence secret set AWS_SECRET_ACCESS_KEY --service backup --stdin
devopsellence secret set DATABASE_URL --service backup --stdin
devopsellence deploy
```

For a local filesystem repository or a restic REST server, replace the AWS
secrets with the credentials that repository type needs.

## Backup

Use `devopsellence exec` to run backup commands inside the backup service.

```bash
devopsellence exec backup -- sh -lc 'restic backup $BACKUP_PATHS --tag app:example --tag env:production'
devopsellence logs backup --lines 100
```

For databases, dump into a temporary file and include that file in the same
restic run. For example, for Postgres:

```bash
devopsellence exec backup -- sh -lc 'pg_dump "$DATABASE_URL" > /tmp/db.sql && restic backup /tmp/db.sql $BACKUP_PATHS --tag app:example --tag env:production'
```

Use an image that contains the tools you need. The official restic image is
fine for simple file backups, but database dumps and shell scripts need
`pg_dump`, `mariadb-dump`, `sqlite3`, or your own small backup image.

## Retention And Checks

Restic already owns retention, pruning, repository integrity, and snapshot
metadata. Keep those policies explicit and run them through the same service.

```bash
devopsellence exec backup -- restic snapshots
devopsellence exec backup -- restic check
devopsellence exec backup -- restic forget --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune
```

An AI operator should be able to report:

- newest successful snapshot age;
- last `restic check` result;
- retention policy;
- repository type without secrets;
- whether a restore drill has been run recently.

## Restore Drill

Practice restores before relying on them.

```bash
devopsellence exec backup -- sh -lc 'rm -rf /tmp/restore-drill && mkdir -p /tmp/restore-drill && restic restore latest --target /tmp/restore-drill'
devopsellence exec backup -- sh -lc 'test -d /tmp/restore-drill/data/app_storage'
```

For database-backed apps, restore into a scratch database first and run an
app-owned check command. Do not restore over production data until you have a
fresh backup, a clear target, and explicit human approval.

## Evidence

Until there is thin command sugar for backup evidence, collect it from restic
and devopsellence primitives:

```bash
devopsellence exec backup -- restic snapshots --json
devopsellence exec backup -- restic check
devopsellence logs backup --lines 200
devopsellence support bundle --output ./devopsellence-support.json
```

`support bundle` redacts devopsellence-managed secrets, but workload logs and
restic command output are raw operational output. Treat them as sensitive.

## Boundary

Build a backup service when your app needs one. Do not add backup-specific
devopsellence concepts unless the ordinary-service pattern proves insufficient.
devopsellence company workflows can wrap the same model with team auth,
policy, scheduled runs, audit trails, and hosted evidence without changing how
the app is backed up on a VM.
