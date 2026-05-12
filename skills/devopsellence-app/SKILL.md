# devopsellence app

Use this skill when building the web app created by `devopsellence vibe`.

Product shape:

- Build one deployable web app, not a stack selection.
- Use Go for the backend.
- Use standard web platform pieces first: HTML, CSS, forms, and small vanilla JavaScript only when it improves the workflow.
- Prefer `net/http`, `html/template`, and SQLite for the first durable version.
- Keep the Dockerfile and `devopsellence.yml` current as the app changes.
- Keep local setup optional: a user with Docker can deploy, while `mise install` is a convenience for local Go development.

Development loop:

1. Preserve the generated app as a working baseline before adding features.
2. Run `go test ./...` after backend changes when Go is available.
3. Run `docker build .` when Docker is available and deployment files changed.
4. Keep the app usable without a frontend build step unless the user explicitly asks for one.
