# frozen_string_literal: true

require "fileutils"
require "minitest/autorun"
require "tmpdir"

class IndexPHPTemplateSmokeTest < Minitest::Test
  def test_template_contains_deployable_index_php_app
    root = File.expand_path("../root", __dir__)

    Dir.mktmpdir("devopsellence-index-php-template-") do |dir|
      app = File.join(dir, "index_php_app")
      FileUtils.cp_r(root, app)

      assert_path_exists File.join(app, ".mise.toml")
      assert_path_exists File.join(app, "Dockerfile")
      assert_path_exists File.join(app, "devopsellence.yml")
      assert_path_exists File.join(app, "public", "index.php")
      assert_path_exists File.join(app, "scripts", "check")
      assert File.executable?(File.join(app, "scripts", "check"))

      index = File.read(File.join(app, "public", "index.php"))
      dockerfile = File.read(File.join(app, "Dockerfile"))
      devopsellence_config = File.read(File.join(app, "devopsellence.yml"))

      assert_includes index, "PRAGMA journal_mode=WAL"
      assert_includes index, "/healthz"
      assert_includes index, "new PDO('sqlite:'"
      assert_includes dockerfile, "FROM nginx:latest"
      assert_includes dockerfile, "php-fpm"
      assert_includes dockerfile, "php-sqlite3"
      assert_includes dockerfile, "clear_env = no"
      assert_includes dockerfile, "try_files $uri /index.php$is_args$args"
      assert_includes dockerfile, "CMD [\"start-index-php\"]"
      assert_includes devopsellence_config, "target: /app/data"
      assert_includes devopsellence_config, "path: /healthz"
      refute_includes devopsellence_config, "postgres"
    end
  end
end
