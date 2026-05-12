# {{APP_NAME}}

A devopsellence-ready web app: Go backend, vanilla HTML/CSS, SQLite storage, and a Dockerfile.

## Local development

```sh
mise install
mise run dev
```

Or use an existing Go install:

```sh
go run .
```

The app listens on `http://localhost:8080`.

## Check

```sh
mise run test
./scripts/check
```

## Deploy

```sh
devopsellence deploy --dry-run
devopsellence deploy
```
