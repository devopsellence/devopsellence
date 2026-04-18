# frozen_string_literal: true

require "test_helper"

class ReleaseTest < ActiveSupport::TestCase
  test "requires explicit ingress service when multiple web services cannot infer a primary" do
    release = build_release(
      runtime_json: release_runtime_json(
        services: {
          "admin" => web_service_runtime(roles: [ "admin" ]),
          "public" => web_service_runtime(roles: [ "public" ])
        },
        ingress_service: nil
      )
    )

    assert_not release.valid?
    assert_includes release.errors[:runtime_json], "ingress_service is required when multiple web services are defined"
  end

  test "uses canonical web service as inferred ingress service when present" do
    release = build_release(
      runtime_json: release_runtime_json(
        services: {
          "admin" => web_service_runtime(roles: [ "admin" ]),
          "web" => web_service_runtime(roles: [ "web" ])
        },
        ingress_service: nil
      )
    )

    assert_predicate release, :valid?
    assert_equal "web", release.ingress_service_name
  end

  test "release task command and entrypoint must be strings" do
    release = build_release(
      runtime_json: release_runtime_json(
        tasks: {
          "release" => {
            "service" => "web",
            "command" => [ "bin/rails", "db:migrate" ],
            "entrypoint" => [ "/bin/sh" ]
          }
        }
      )
    )

    assert_not release.valid?
    assert_includes release.errors[:runtime_json], "tasks.release.command must be a string"
    assert_includes release.errors[:runtime_json], "tasks.release.entrypoint must be a string"
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
end
