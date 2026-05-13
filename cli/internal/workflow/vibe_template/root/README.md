# {{APP_NAME}}

A devopsellence-ready web app: Go backend, vanilla HTML/CSS, SQLite storage, and a Dockerfile.

## Local development

```sh
./scripts/dev
```

The app listens on `http://localhost:18080` and stores development data at
`/tmp/{{APP_NAME}}.sqlite`.

To smoke-test a running local server:

```sh
./scripts/smoke
```

## Check

```sh
./scripts/check
```

The check runs Go tests, Docker test/build targets, and a devopsellence dry-run
when the CLI is available. If no node is attached yet, the expected dry-run
blocker is accepted.

The app can also be run through Docker:

```sh
docker build -t {{APP_NAME}}:local .
docker run --rm -p 8080:8080 -v {{APP_NAME}}-data:/data {{APP_NAME}}:local
```

## Deploy

```sh
devopsellence deploy --dry-run
devopsellence deploy
```
