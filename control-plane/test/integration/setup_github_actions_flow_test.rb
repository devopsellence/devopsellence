# frozen_string_literal: true

require "securerandom"
require "test_helper"

class SetupGithubActionsFlowTest < ActionDispatch::IntegrationTest
  test "setup uses default organization and production environment by default" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    sign_in_as(user)

    with_successful_organization_runtime_provisioning do
      Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
        Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
      )

      assert_difference([ "Organization.count", "OrganizationMembership.count", "Project.count", "Environment.count", "ApiToken.count" ], 1) do
        post setup_github_actions_path, params: { project_name: "demo" }
      end
    end

    assert_redirected_to setup_github_actions_path
    follow_redirect!
    assert_response :success

    organization = user.organizations.order(:id).find_by!(name: Organization::DEFAULT_NAME)
    project = organization.projects.find_by!(name: "demo")
    environment = project.environments.find_by!(name: "production")

    assert_equal Organization::DEFAULT_NAME, organization.name
    assert_match "organization: #{organization.name}", response.body
    assert_match "project: #{project.name}", response.body
    assert_match "environment: #{environment.name}", response.body
  end

  test "setup supports custom organization project and environment" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    sign_in_as(user)

    with_successful_organization_runtime_provisioning do
      Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
        Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
      )

      post setup_github_actions_path, params: {
        organization_name: "acme",
        project_name: "demo",
        environment_name: "staging"
      }
    end

    assert_redirected_to setup_github_actions_path
    follow_redirect!
    assert_response :success

    organization = user.organizations.order(:id).find_by!(name: "acme")
    project = organization.projects.find_by!(name: "demo")
    environment = project.environments.find_by!(name: "staging")

    assert_match "organization: #{organization.name}", response.body
    assert_match "project: #{project.name}", response.body
    assert_match "environment: #{environment.name}", response.body
  end

  private

  def sign_in_as(user)
    raw_token = SecureRandom.hex(16)
    LoginLink.create!(
      user: user,
      token_digest: LoginLink.digest(raw_token),
      expires_at: 15.minutes.from_now
    )

    without_http_basic do
      get login_verify_path(token: raw_token)
      assert_redirected_to getting_started_path(anchor: "quickstart-heading")
    end
  end
end
