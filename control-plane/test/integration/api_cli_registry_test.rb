# frozen_string_literal: true

require "test_helper"
require "json"

class ApiCliRegistryTest < ActionDispatch::IntegrationTest
  test "owners can configure standalone org registry and request push auth" do
    with_env(
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
    ) do
      user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      OrganizationMembership.create!(organization:, user:, role: OrganizationMembership::ROLE_OWNER)
      project = organization.projects.create!(name: "Project A")
      _token_record, access_token, = ApiToken.issue!(user: user)

      post "/api/v1/cli/organizations/#{organization.id}/registry",
        params: {
          registry_host: "ghcr.io",
          repository_namespace: "acme/apps",
          username: "robot",
          password: "reg-secret"
        },
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json
      assert_response :created
      assert_equal true, json_body.fetch("configured")
      assert_equal "ghcr.io", json_body.fetch("registry_host")

      post "/api/v1/cli/projects/#{project.id}/registry/push_auth",
        params: { image_repository: "project-a" },
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json
      assert_response :created
      assert_equal "ghcr.io", json_body.fetch("registry_host")
      assert_equal "ghcr.io/acme/apps", json_body.fetch("repository_path")
      assert_equal "robot", json_body.fetch("docker_username")
      assert_equal "reg-secret", json_body.fetch("docker_password")
    end
  end

  private

  def json_body
    JSON.parse(response.body)
  end
end
