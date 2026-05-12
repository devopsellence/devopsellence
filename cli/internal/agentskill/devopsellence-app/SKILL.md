---
name: devopsellence-app
description: Build the Go and vanilla web app baseline created by devopsellence vibe.
homepage: https://www.devopsellence.com
---

# devopsellence app

Use this skill when building the web app created by `devopsellence vibe`.

Product shape:

- Build one deployable web app, not a stack selection.
- Use Go for the backend.
- Use standard web platform pieces first: HTML, CSS, forms, and small vanilla JavaScript only when it improves the workflow.
- Prefer `net/http`, `html/template`, and SQLite for the first durable version.
- Keep the Dockerfile and `devopsellence.yml` current as the app changes.
- Keep Docker as the required local tool. Go may be installed locally, but the Dockerfile owns the portable build and test path.

Development loop:

1. Preserve the generated app as a working baseline before adding features.
2. Run `docker build --target test .` after backend changes.
3. Run `docker build .` when deployment files changed.
4. Keep the app usable without a frontend build step unless the user explicitly asks for one.
