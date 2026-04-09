# frozen_string_literal: true

require "json"
require "rack/utils"
require "securerandom"
require "test_helper"
require "uri"

class DashboardManagementTest < ActionDispatch::IntegrationTest
  self.use_transactional_tests = false

  test "dashboard create routes redirect to getting started without side effects" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    sign_in_as(user)

    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "api")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-1")
    release = project.releases.create!(
      git_sha: "abcd1234",
      revision: "rel-1",
      image_repository: "api",
      image_digest: "sha256:abc",
      web_json: { "port" => 3000, "healthcheck" => { "path" => "/up", "port" => 3000 } }.to_json
    )

    assert_no_difference("Organization.count") do
      post dashboard_organizations_path, params: { name: "new-org" }
    end
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")

    assert_no_difference("Project.count") do
      post dashboard_projects_path(organization), params: { name: "new-api" }
    end
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")

    assert_no_difference("Environment.count") do
      post dashboard_environments_path(project), params: { name: "staging" }
    end
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")

    assert_no_difference("Deployment.count") do
      post dashboard_publish_release_path(release), params: { environment_id: environment.id }
    end
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")

    assert_no_changes -> { node.reload.environment_id } do
      post dashboard_assignments_path(environment), params: { node_id: node.id }
    end
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")

    assert_no_changes -> { node.reload.labels } do
      post dashboard_node_labels_path(node), params: { labels: "web,worker" }
    end
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")

    assert_no_difference("EnvironmentSecret.count") do
      post dashboard_environment_secrets_path(environment), params: {
        service_name: "web",
        name: "SECRET_KEY_BASE",
        value: "super-secret"
      }
    end
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")
  end

  test "dashboard redirects contributors to getting started" do
    owner = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    contributor = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "shared-org")
    OrganizationMembership.create!(organization: organization, user: owner, role: "owner")
    OrganizationMembership.create!(organization: organization, user: contributor, role: "contributor")

    sign_in_as(contributor)

    assert_no_difference("Project.count") do
      post dashboard_projects_path(organization), params: { name: "api" }
    end

    assert_redirected_to getting_started_path(anchor: "quickstart-heading")
  end

  test "dashboard label updates are disabled" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-1")

    sign_in_as(user)

    post dashboard_node_labels_path(node), params: { labels: "web,worker" }

    assert_redirected_to getting_started_path(anchor: "quickstart-heading")
    assert_equal [Node::LABEL_WEB], node.reload.labels
  end

  test "signed in user hitting dashboard is redirected to getting started and can still sign out" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    sign_in_as(user)

    get dashboard_path
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")

    delete logout_path

    assert_redirected_to root_path
    get dashboard_path
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")
  end

  test "dashboard secret writes are disabled" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "api")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )

    sign_in_as(user)

    assert_no_difference("EnvironmentSecret.count") do
      post dashboard_environment_secrets_path(environment), params: {
        service_name: "web",
        name: "SECRET_KEY_BASE",
        value: "super-secret"
      }
    end

    assert_redirected_to getting_started_path(anchor: "quickstart-heading")
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
