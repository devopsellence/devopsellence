# frozen_string_literal: true

APP_NAME = app_name.tr("_", "-")
RUBY_VERSION = "4.0.3"
NODE_VERSION = "24"
MISE_AVAILABLE = system("mise", "--version", out: File::NULL, err: File::NULL)

def ensure_gem(name, *args, **options)
  return if File.read("Gemfile").match?(/^\s*gem ["']#{Regexp.escape(name)}["']/)

  gem name, *args, **options
end

def run_mise!(*command)
  say_status :run, command.join(" ")
  abort "#{command.join(" ")} failed" unless system(*command)
end

def bundled_with_version
  lines = File.readlines("Gemfile.lock")
  marker_index = lines.index { |line| line.strip == "BUNDLED WITH" }
  return unless marker_index

  lines[marker_index + 1]&.strip
end

def remove_kamal!
  gsub_file "Gemfile", /^# Deploy this application anywhere as a Docker container \[https:\/\/kamal-deploy\.org\]\n/, ""
  gsub_file "Gemfile", /^gem ["']kamal["'], require: false\n/, ""
  gsub_file "Dockerfile", "Use with Kamal or build'n'run by hand", "Use with devopsellence or build'n'run by hand"
  remove_file "config/deploy.yml"
  remove_file "bin/kamal"
  remove_dir ".kamal"
end

ensure_gem "tailwindcss-rails"
ensure_gem "bcrypt"
ensure_gem "pundit"

gem_group :development, :test do
  ensure_gem "brakeman", require: false
  ensure_gem "bundler-audit", require: false
  ensure_gem "rubocop-rails-omakase", require: false
end

# Rails ships with Kamal by default, but devopsellence is the blessed deploy
# path for this template. Remove the second deployment system before bundle
# install and again after Rails' own after-bundle hooks have had a chance to
# recreate Kamal config/secrets files.
remove_kamal!

file ".mise.toml", <<~TOML
  [tools]
  ruby = "#{RUBY_VERSION}"
  node = "#{NODE_VERSION}"
TOML
file ".ruby-version", "ruby-#{RUBY_VERSION}\n", force: true

if MISE_AVAILABLE
  say_status :run, "mise trust .mise.toml"
  say_status :warn, "mise trust failed; run `mise trust` after scaffold" unless system("mise", "trust", ".mise.toml")
end

file ".agents/skills/devopsellence-rails-app/SKILL.md", <<~'SKILL'
  ---
  name: devopsellence-rails-app
  description: Use when building, modifying, testing, deploying, or scaling the blessed devopsellence Rails application baseline. Covers Rails 8.1, SQLite-first MVPs, Hotwire, Tailwind, Solid Queue/Cache/Cable, stack expansion, security checks, Docker, mise, and devopsellence solo on Linux servers.
  ---

  # devopsellence Rails App

  Use this skill inside apps generated from the devopsellence Rails template.

  ## Defaults

  - Rails 8.1, Ruby 4.0+, SQLite, Puma, Thruster, Hotwire, Turbo, Stimulus, ERB, importmap, Propshaft, and Tailwind.
  - Solid Queue, Solid Cache, and Solid Cable before Redis or Sidekiq.
  - Keep the first MVP local and portable: SQLite, file-backed development defaults, no external service dependency unless the product need is explicit.
  - Active Storage only when uploads are part of the product workflow. Use local storage first, then S3-compatible object storage when durability or multiple nodes require it.
  - Built-in Rails authentication with `bcrypt`; use Pundit for authorization.
  - ViewComponent, Pagy, lucide icons, Brakeman, bundler-audit, and rubocop-rails-omakase when they fit the app's actual UI, pagination, icon, security, or lint needs.
  - Minitest, fixtures, Capybara system tests, and focused integration tests.
  - `mise` owns language/tool versions. Do not replace it with ad hoc local setup docs.

  ## Do Not Add By Default

  - Devise, Sidekiq, Redis, React, Next.js, Vite, GraphQL, Elasticsearch, Meilisearch, Kubernetes, or an admin framework.
  - Extra gems, hosted services, or observability vendors before the app has a real product need.
  - A second deployment system. Use devopsellence for deploy, secrets, logs, status, rollback, and node operations.

  ## Build Loop

  1. Inspect the app and the user's request.
  2. Keep changes idiomatic Rails and close to the behavior.
  3. Add or update focused tests.
  4. Run the narrowest useful command first, then broader checks before handoff:
     - `mise install`
     - `mise exec -- bundle install`
     - `bin/rails db:prepare`
     - `bin/rails test`
     - `bin/rails test:system` when UI behavior changed
     - `bin/brakeman`
     - `bundle exec bundler-audit check --update`
  5. Keep production concerns wired while building features: health checks, background jobs, uploads, secrets, logs, and deploy config.

  ## Stack Expansion

  Start with SQLite and the smallest deployable Rails shape. When the MVP has real production pressure, add capabilities deliberately:

  - Backups and restore drills: follow https://docs.devopsellence.com/guides/backup-restore/ before risky migrations, data imports, or production cutovers.
  - PostgreSQL: move to managed or dedicated PostgreSQL when concurrency, data size, operations, reporting, extensions, or team practices outgrow SQLite.
  - Durable uploads: move Active Storage to S3-compatible object storage when uploaded files must survive node replacement or be shared across nodes.
  - Email: add a transactional email provider only when the product sends real user-facing mail.
  - Monitoring: add Sentry and OpenTelemetry when production error reporting, traces, or alerting are needed.
  - DNS/CDN: add Cloudflare DNS/CDN after the user confirms the zone, hostname, and mutation plan.

  Keep every expansion visible in the implementation plan. Prefer explicit follow-up tasks over silently adding services during the first feature slice.

  ## Production Shape

  - Start with one Rails web process, SQLite, Solid tables, JSON logs, and devopsellence deploy.
  - Scale to medium-company shape by adding web nodes, splitting workers, moving PostgreSQL to managed or dedicated infrastructure, adding object storage, adding Sentry/OpenTelemetry, and separating Solid Queue/Cache/Cable databases or pools when pressure appears.
  - Keep ordinary-tool escape hatches visible: SSH, Docker, logs, files, SQL, JSON, and cloud CLIs.

  ## devopsellence Loop

  1. Configure `devopsellence.yml` with explicit services, ports, health checks, and worker processes.
  2. Store production secrets with `devopsellence secret set --stdin`; never commit secret values.
  3. Run `devopsellence deploy --dry-run` before production mutations.
  4. After deploy, collect `devopsellence status`, app logs, node logs, and HTTPS evidence when ingress is configured.
SKILL

file "devopsellence.yml", <<~YAML
  schema_version: 1
  organization: solo
  project: #{APP_NAME}
  default_environment: production

  build:
    context: .
    dockerfile: Dockerfile
    platforms:
      - linux/amd64

  services:
    web:
      ports:
        - name: http
          port: 80
      healthcheck:
        path: /up
        port: 80
      volumes:
        - source: #{APP_NAME}-storage
          target: /rails/storage
      env:
        RAILS_ENV: production
        SOLID_QUEUE_IN_PUMA: "true"
      secret_refs:
        - name: SECRET_KEY_BASE
          secret: SECRET_KEY_BASE

  tasks:
    release:
      service: web
      command:
        - ./bin/rails
        - db:prepare
YAML

file "bin/jobs", <<~'RUBY'
  #!/usr/bin/env ruby

  require_relative "../config/environment"
  require "solid_queue/cli"

  SolidQueue::Cli.start(ARGV)
RUBY
chmod "bin/jobs", 0o755

environment "config.active_job.queue_adapter = :solid_queue", env: "production"
environment "config.solid_queue.connects_to = { database: { writing: :queue } }", env: "production"

after_bundle do
  unless options[:skip_bundle]
    rails_command "tailwindcss:install"
    rails_command "solid_queue:install"
    rails_command "solid_cache:install"
    rails_command "solid_cable:install"
    generate "pundit:install"
    if MISE_AVAILABLE
      run_mise! "mise", "install"
      if (version = bundled_with_version)
        run_mise! "mise", "exec", "--", "gem", "install", "bundler", "-v", version
      end
      run_mise! "mise", "exec", "--", "bundle", "install"
      run_mise! "mise", "exec", "--", "bundle", "check"
    end
    remove_kamal!
  end
end
