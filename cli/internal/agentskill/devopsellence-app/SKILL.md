---
name: devopsellence-app
description: Build the Go and vanilla web app baseline created by devopsellence vibe.
homepage: https://www.devopsellence.com
---

# devopsellence app

Use this skill when building or iterating on the web app created by
`devopsellence vibe`.

Mission:

- Turn the user's idea into one polished, deployable native-web app.
- Keep the app understandable enough that a human can inspect, edit, deploy,
  and revert it without learning a framework-specific stack.
- Treat the generated app as a durable product baseline, not a throwaway demo.

Hard constraints:

- Use Go, `net/http`, `html/template`, SQLite, semantic HTML, handcrafted CSS,
  and small vanilla JavaScript.
- Do not add React, Vue, Svelte, Next, Astro, HTMX, Tailwind, npm, Vite,
  bundlers, transpilers, frontend package managers, or CDN UI kits unless the
  user explicitly overrides this constraint.
- Do not create a frontend build step.
- Prefer server-rendered pages, HTML forms, POST/redirect/GET, and progressive
  enhancement before client-side state.
- Keep Docker as the portable local tool. Go may be installed locally, but the
  Dockerfile owns the repeatable build and test path.
- Keep `devopsellence.yml`, the Dockerfile, health checks, ports, and persistent
  volume wiring current as the app changes.

Work loop:

1. Understand the user's app idea and treat the generated app shell as a
   deploy-ready starting point, not product behavior.
2. Derive the smallest real product workflow that proves the app concept.
3. Make a short implementation plan with data model, pages, actions, and states.
4. After confirmation when the prompt requires it, delete or rewrite generated
   shell code, routes, content, styles, and tests that do not serve the idea.
5. Implement in small reversible slices; commit/checkpoint before risky changes
   when git is available.
6. After each slice, run a subtraction pass: remove unused routes, handlers,
   styles, helpers, placeholder content, stale tests, and speculative
   abstractions while preserving user-confirmed behavior.
7. If Go is installed locally, run `go test ./...` after backend or data
   changes for fast iteration.
8. Run `./scripts/check` before treating the app as deploy-ready; this is the
   portable baseline and uses Docker test/build targets when local Go is
   unavailable.
9. Run `./scripts/smoke` against a running local server after UI or HTTP changes.
10. Keep the app deployable after every feature slice.

Product shaping:

- Start from the user's actual workflow, not generic CRUD scaffolding.
- Name domain concepts in user language.
- Build complete first-use, empty, success, validation, and error states.
- Prefer one coherent app flow over many shallow pages.
- Mark larger ideas as follow-ups instead of adding hidden abstractions early.
- Prefer subtraction and clearer existing code over new abstractions; add
  structure only when it removes real complexity.

Go implementation:

- Keep handlers small and explicit.
- Pass `context.Context` into database work and use bounded timeouts for writes
  or reads that can block.
- Check and return errors deliberately; use redirects for successful form
  submissions.
- Keep schema migrations simple and append-only in code until the user needs a
  migration system.
- Write table-driven tests for handlers, validation, persistence, and security
  boundaries that matter.
- Do not add goroutines, background jobs, queues, caches, or service layers
  unless the product need is clear.

Pattern snippets:

Use these as shape examples, not as product scaffolding to preserve.

Root route:

```go
func (a *app) home(w http.ResponseWriter, r *http.Request) {
  var body bytes.Buffer
  if err := a.templates.ExecuteTemplate(&body, "index.html", data); err != nil {
    http.Error(w, "could not render page", http.StatusInternalServerError)
    return
  }
  w.Header().Set("Content-Type", "text/html; charset=utf-8")
  _, _ = body.WriteTo(w)
}
```

SQLite-backed readiness:

```go
func (a *app) health(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
  defer cancel()
  var ok int
  if err := a.db.QueryRowContext(ctx, `SELECT 1`).Scan(&ok); err != nil {
    http.Error(w, "database unavailable", http.StatusServiceUnavailable)
    return
  }
  w.WriteHeader(http.StatusNoContent)
}
```

Handler test shape:

```go
response := httptest.NewRecorder()
request := httptest.NewRequest(http.MethodGet, "/", nil)
application.routes().ServeHTTP(response, request)
if response.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d", response.Code)
}
```

Static traversal guard:

```go
response := httptest.NewRecorder()
request := httptest.NewRequest(http.MethodGet, "/static/../templates/index.html", nil)
application.routes().ServeHTTP(response, request)
if response.Code == http.StatusOK {
  t.Fatal("static traversal returned 200")
}
```

Native UI craft:

- Use semantic HTML first: forms, labels, buttons, headings, tables, lists,
  `dialog`, `details`, and native validation where they fit.
- Use CSS custom properties, modern selectors, grid, flexbox, container queries,
  and media queries for responsive layouts.
- Choose a clear visual direction that matches the app's domain, but keep it
  practical and maintainable.
- Avoid generic AI card soup, placeholder-heavy layouts, oversized marketing
  sections, and decorative effects that obscure the workflow.
- Add vanilla JavaScript only for direct interaction wins such as optimistic
  affordances, dialogs, inline filtering, previews, or keyboard ergonomics.
- Keep JavaScript optional where reasonable; the core workflow should survive a
  plain form submit.

Deploy readiness:

- Keep `/healthz` fast and tied to real app readiness.
- Keep runtime configuration in env vars and `devopsellence.yml`, not hardcoded
  secrets.
- Keep persistent data under `/data` when deploying with the generated volume.
- Keep `scripts/dev`, `scripts/smoke`, and `scripts/check` current as routes,
  text, health behavior, ports, or deploy config change.
- Before handoff, the machine-checkable baseline is `./scripts/check`, local
  smoke when HTTP behavior changed, and `devopsellence deploy --dry-run`. Use
  `go test ./...` as an additional fast local check when Go is installed.
- If no server is selected yet, the acceptable dry-run blocker is the explicit
  no-node/no-attachment message. Mode unset, invalid config, build failure, or
  health mismatch is not acceptable.
- Use `devopsellence deploy --dry-run` before a real deploy.
- After deploy, report status, logs, health, and public URL evidence when
  ingress is configured.
