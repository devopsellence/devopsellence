# frozen_string_literal: true

require "minitest/autorun"
require "tmpdir"

class TemplateSmokeTest < Minitest::Test
  def test_template_generates_devopsellence_files
    template = File.expand_path("../template.rb", __dir__)

    Dir.mktmpdir("devopsellence-rails-template-") do |dir|
      app = File.join(dir, "smoke_app")
      assert system("rails", "new", app, "-d", "sqlite3", "-m", template, "--skip-bundle"), "rails new failed"

      assert_path_exists File.join(app, ".mise.toml")
      assert_equal "ruby-4.0.3\n", File.read(File.join(app, ".ruby-version"))
      assert_path_exists File.join(app, "devopsellence.yml")
      assert_path_exists File.join(app, ".agents", "skills", "devopsellence-rails-app", "SKILL.md")
      skill = File.read(File.join(app, ".agents", "skills", "devopsellence-rails-app", "SKILL.md"))
      gemfile = File.read(File.join(app, "Gemfile"))
      dockerfile = File.read(File.join(app, "Dockerfile"))
      devopsellence_config = File.read(File.join(app, "devopsellence.yml"))

      assert_includes skill, "SQLite-first MVPs"
      assert_includes skill, "Stack Expansion"
      assert_includes dockerfile, "EXPOSE 80"
      assert_includes devopsellence_config, "port: 80"
      assert_includes devopsellence_config, "target: /rails/storage"
      assert_includes devopsellence_config, "source: smoke-app-storage"
      assert_includes gemfile, "gem \"pundit\""
      refute_includes gemfile, "kamal"
      refute_includes gemfile, "sentry-rails"
      refute_includes gemfile, "opentelemetry"
      refute_includes gemfile, "aws-sdk-s3"
      refute_includes dockerfile, "Kamal"
      refute_path_exists File.join(app, "config", "deploy.yml")
      refute_path_exists File.join(app, "bin", "kamal")
      refute_path_exists File.join(app, ".kamal")
      assert_includes devopsellence_config, "SOLID_QUEUE_IN_PUMA"
    end
  end
end
