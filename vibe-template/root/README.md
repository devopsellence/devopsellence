# {{APP_NAME}}

A devopsellence-ready web app: Go backend, vanilla HTML/CSS, SQLite storage, and a Dockerfile.

## Local development

```sh
docker build --target test .
docker build -t {{APP_NAME}}:local .
docker run --rm -p 8080:8080 -v {{APP_NAME}}-data:/data {{APP_NAME}}:local
```

The app listens on `http://localhost:8080`.

If Go is installed locally:

```sh
go run .
```

## Check

```sh
./scripts/check
```

## Deploy

```sh
devopsellence deploy --dry-run
devopsellence deploy
```
