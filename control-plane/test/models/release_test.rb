# frozen_string_literal: true

require "test_helper"

class ReleaseTest < ActiveSupport::TestCase
  test "accepts releases without ingress config" do
    release = build_release(
      runtime_json: JSON.generate(
        {
          "services" => {
            "admin" => web_service_runtime,
            "public" => web_service_runtime
          }
        }
      )
    )

    assert_predicate release, :valid?
    assert_equal [], release.ingress_target_service_names
  end

  test "uses configured ingress target services" do
    release = build_release(
      runtime_json: release_runtime_json(
        services: {
          "admin" => web_service_runtime,
          "web" => web_service_runtime
        },
        ingress: {
          "hosts" => ["app.example.com"],
          "rules" => [
            {
              "match" => { "host" => "app.example.com", "path_prefix" => "/" },
              "target" => { "service" => "web", "port" => "http" }
            }
          ]
        }
      )
    )

    assert_predicate release, :valid?
    assert_equal ["web"], release.ingress_target_service_names
    assert_equal "web", release.ingress_service_name
  end

  test "release task command and args must be arrays" do
    release = build_release(
      runtime_json: release_runtime_json(
        tasks: {
          "release" => {
            "service" => "web",
            "command" => "bin/rails",
            "args" => "db:migrate"
          }
        }
      )
    )

    assert_not release.valid?
    assert_includes release.errors[:runtime_json], "tasks.release.command must be an array of strings"
    assert_includes release.errors[:runtime_json], "tasks.release.args must be an array of strings"
  end

  test "accepts explicit ingress rules targeting generic services and custom ports" do
    release = build_release(
      runtime_json: JSON.generate(
        {
          "services" => {
            "app" => {
              "ports" => [{ "name" => "http", "port" => 3000 }],
              "healthcheck" => { "path" => "/up", "port" => 3000 }
            },
            "api" => {
              "ports" => [{ "name" => "metrics", "port" => 9090 }]
            }
          },
          "ingress" => {
            "hosts" => ["app.example.com"],
            "rules" => [
              {
                "match" => { "host" => "app.example.com", "path_prefix" => "/api" },
                "target" => { "service" => "api", "port" => "metrics" }
              },
              {
                "match" => { "host" => "app.example.com", "path_prefix" => "/" },
                "target" => { "service" => "app", "port" => "http" }
              }
            ]
          }
        }
      )
    )

    assert_predicate release, :valid?
  end

  test "rejects non-object ingress payloads" do
    release = build_release(
      runtime_json: JSON.generate(
        {
          "services" => {
            "web" => web_service_runtime
          },
          "ingress" => "web"
        }
      )
    )

    assert_not release.valid?
    assert_includes release.errors[:runtime_json], "ingress must be an object"
  end

  test "blank kind still infers required labels from service shape" do
    release = build_release(
      runtime_json: release_runtime_json(
        services: {
          "web" => web_service_runtime.merge("kind" => "")
        }
      )
    )

    assert_equal ["web"], release.required_labels
    assert_predicate release, :valid?
  end

  test "scheduled_services_for fails fast for stored legacy string command" do
    release = persisted_release(
      runtime_json: release_runtime_json(
        services: {
          "web" => web_service_runtime(command: "./bin/server")
        }
      )
    )

    error = assert_raises(Release::InvalidRuntimeConfig) do
      release.scheduled_services_for(node: build_node_for(release:))
    end

    assert_equal "services.web.command must be an array of strings", error.message
  end

  test "release_task_for fails fast for stored deprecated entrypoint" do
    release = persisted_release(
      runtime_json: release_runtime_json(
        tasks: {
          "release" => {
            "service" => "web",
            "entrypoint" => [ "/bin/sh" ],
            "command" => [ "bundle", "exec", "rails", "db:migrate" ]
          }
        }
      )
    )

    error = assert_raises(Release::InvalidRuntimeConfig) do
      release.release_task_for(node: build_node_for(release:))
    end

    assert_equal "tasks.release.entrypoint is no longer supported; use command or args", error.message
  end

  private

  def build_release(runtime_json:)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    project = organization.projects.create!(name: "api")
    project.releases.new(
      git_sha: "a" * 40,
      image_repository: "api",
      image_digest: "sha256:#{"b" * 64}",
      runtime_json: runtime_json
    )
  end

  def persisted_release(runtime_json:)
    release = build_release(runtime_json:)
    release.save!(validate: false)
    release
  end

  def build_node_for(release:)
    organization = release.project.organization
    ensure_test_organization_runtime!(organization)
    environment = release.project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-#{SecureRandom.hex(3)}",
      gcp_project_number: SecureRandom.random_number(10**12).to_s,
      service_account_email: "svc-#{SecureRandom.hex(4)}@example.test",
      workload_identity_pool: "pool-#{SecureRandom.hex(3)}",
      workload_identity_provider: "provider-#{SecureRandom.hex(3)}",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    node, _access, _refresh = issue_test_node!(organization:, name: "node-#{SecureRandom.hex(3)}")
    node.update!(environment:)
    node
  end
end
