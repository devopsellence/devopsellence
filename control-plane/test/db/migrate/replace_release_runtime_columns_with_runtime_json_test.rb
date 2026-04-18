# frozen_string_literal: true

require "test_helper"
require Rails.root.join("db/migrate/20260418123000_replace_release_runtime_columns_with_runtime_json").to_s

class ReplaceReleaseRuntimeColumnsWithRuntimeJsonTest < ActiveSupport::TestCase
  test "converts legacy web worker and release command into schema v5 runtime payload" do
    runtime = ReplaceReleaseRuntimeColumnsWithRuntimeJson.new.send(
      :legacy_runtime_payload,
      web: {
        "port" => 9292,
        "healthcheck" => { "path" => "/up", "port" => 9292 },
        "command" => "bin/server",
        "env" => { "RAILS_ENV" => "production" }
      },
      worker: {
        "command" => "bin/jobs",
        "env" => { "QUEUE" => "default" }
      },
      release_command: "bin/rails db:migrate"
    )

    assert_equal "web", runtime.fetch("ingress_service")
    assert_equal "web", runtime.dig("services", "web", "kind")
    assert_equal [ "web" ], runtime.dig("services", "web", "roles")
    assert_equal [ { "name" => "http", "port" => 9292 } ], runtime.dig("services", "web", "ports")
    assert_equal({ "path" => "/up", "port" => 9292 }, runtime.dig("services", "web", "healthcheck"))
    assert_equal "worker", runtime.dig("services", "worker", "kind")
    assert_equal [ "worker" ], runtime.dig("services", "worker", "roles")
    assert_equal "bin/rails db:migrate", runtime.dig("tasks", "release", "command")
    assert_equal "web", runtime.dig("tasks", "release", "service")
  end
end
