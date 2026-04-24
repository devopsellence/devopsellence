# frozen_string_literal: true

require "test_helper"

class ReleaseTest < ActiveSupport::TestCase
  test "requires explicit ingress service when multiple web services cannot infer a primary" do
    release = build_release(
      runtime_json: release_runtime_json(
        services: {
          "admin" => web_service_runtime,
          "public" => web_service_runtime
        },
        ingress: nil
      )
    )

    assert_not release.valid?
    assert_includes release.errors[:runtime_json], "ingress.service is required when multiple web services are defined"
  end

  test "requires explicit ingress service even when a canonical web service exists" do
    release = build_release(
      runtime_json: release_runtime_json(
        services: {
          "admin" => web_service_runtime,
          "web" => web_service_runtime
        },
        ingress: nil
      )
    )

    assert_not release.valid?
    assert_includes release.errors[:runtime_json], "ingress.service is required when multiple web services are defined"
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

  test "ingress hosts must be unique case-insensitively" do
    release = build_release(
      runtime_json: release_runtime_json(
        ingress: {
          "service" => "web",
          "hosts" => ["App.Example.Test", "app.example.test"]
        }
      )
    )

    assert_not release.valid?
    assert_includes release.errors[:runtime_json], "ingress.hosts must be unique"
  end

  test "ingress must decode to an object when present" do
    release = build_release(
      runtime_json: JSON.generate(
        {
          "services" => { "web" => web_service_runtime },
          "tasks" => {},
          "ingress" => "web"
        }
      )
    )

    assert_not release.valid?
    assert_includes release.errors[:runtime_json], "ingress must decode to an object"
  end

  test "blank kind does not contribute required labels and reports one kind error" do
    release = build_release(
      runtime_json: release_runtime_json(
        services: {
          "web" => web_service_runtime.merge("kind" => "")
        }
      )
    )

    assert_equal [], release.required_labels
    assert_not release.valid?
    kind_errors = release.errors[:runtime_json].grep(/\Aservices\.web\.kind /)
    assert_equal [ "services.web.kind must be present" ], kind_errors
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
