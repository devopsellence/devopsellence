# devopsellence Rails app template

Blessed Rails baseline for `devopsellence vibe`.

```sh
rails new my-app \
  -d sqlite3 \
  -m https://raw.githubusercontent.com/devopsellence/devopsellence/master/vibe-templates/rails-app/template.rb
```

The template creates a Rails 8.1 app with SQLite, Hotwire, Tailwind, Solid
Queue/Cache/Cable, security checks, Docker, `devopsellence.yml`,
`.mise.toml`, and `.agents/skills/devopsellence-rails-app`.

`root/` is the bundled fallback scaffold used when `devopsellence vibe` runs
without a local `rails` executable. The CLI keeps an embedded mirror of this
directory for release binaries, and tests check the mirror stays in sync.

Run the smoke test:

```sh
ruby test/template_smoke_test.rb
```
