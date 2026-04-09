# frozen_string_literal: true

require "json"
require "securerandom"
require "test_helper"

class ApiCliAnonymousTrialTest < ActionDispatch::IntegrationTest
  include ActiveJob::TestHelper

  setup do
    clear_enqueued_jobs
    clear_performed_jobs
    ActionMailer::Base.deliveries.clear
  end

  test "public bootstrap creates anonymous user and issues tokens" do
    assert_difference([ "User.count", "ApiToken.count" ], 1) do
      post "/api/v1/public/cli/bootstrap",
        params: {
          anonymous_id: "anon-123",
          anonymous_secret: "secret-123",
          client_id: "cli"
        },
        as: :json
    end

    assert_response :created
    assert_equal "anonymous", json_body.fetch("account_kind")

    user = User.order(:id).last
    assert user.anonymous?
    assert_equal "anon-123", user.anonymous_identifier
    assert user.anonymous_secret_matches?("secret-123")
  end

  test "public bootstrap reuses existing anonymous user when secret matches" do
    user = User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")

    assert_no_difference("User.count") do
      assert_difference("ApiToken.count", 1) do
        post "/api/v1/public/cli/bootstrap",
          params: {
            anonymous_id: "anon-123",
            anonymous_secret: "secret-123",
            client_id: "cli"
          },
          as: :json
      end
    end

    assert_response :created
    assert_equal user.id, ApiToken.order(:id).last.user_id
  end

  test "public bootstrap rejects wrong anonymous secret" do
    User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")

    post "/api/v1/public/cli/bootstrap",
      params: {
        anonymous_id: "anon-123",
        anonymous_secret: "wrong-secret",
        client_id: "cli"
      },
      as: :json

    assert_response :unauthorized
    assert_equal "invalid anonymous_secret", json_body.fetch("error_description")
  end

  test "anonymous user can create exactly one trial organization" do
    user = User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")

    with_runtime_config(managed_pool_target: "2") do
      with_successful_organization_runtime_provisioning do
        assert_enqueued_with(job: Runtime::EnsureBundlesJob) do
          assert_difference([ "Organization.count", "OrganizationMembership.count" ], 1) do
            post "/api/v1/cli/organizations",
              params: { name: "trial-org" },
              headers: auth_headers_for(user),
              as: :json
          end
        end
      end
    end

    assert_response :created
    assert_equal "trial", json_body.fetch("plan_tier")

    post "/api/v1/cli/organizations",
      params: { name: "second-org" },
      headers: auth_headers_for(user),
      as: :json

    assert_response :forbidden
    assert_equal "trial accounts support a single organization", json_body.fetch("error_description")
  end

  test "deploy target reuses the existing trial organization when organization is omitted" do
    user = User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")
    organization = Organization.create!(name: "trial-org", plan_tier: Organization::PLAN_TIER_TRIAL)
    ensure_test_organization_runtime!(organization)
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    assert_enqueued_with(job: Runtime::EnsureBundlesJob) do
      post "/api/v1/cli/deploy_target",
        params: {
          project: "ShopApp",
          environment: "production"
        },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :created
    assert_equal false, json_body.fetch("organization_created")
    assert_equal true, json_body.fetch("project_created")
    assert_equal true, json_body.fetch("environment_created")
    assert_equal organization.id, json_body.dig("organization", "id")
    assert_equal "trial", json_body.dig("organization", "plan_tier")
    assert_equal "managed", json_body.dig("environment", "runtime_kind")
  end

  test "deploy target cannot create a second trial organization" do
    user = User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")
    organization = Organization.create!(name: "trial-org", plan_tier: Organization::PLAN_TIER_TRIAL)
    ensure_test_organization_runtime!(organization)
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)

    post "/api/v1/cli/deploy_target",
      params: {
        organization: "second-org",
        project: "ShopApp",
        environment: "production"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :forbidden
    assert_equal "trial accounts support a single organization", json_body.fetch("error_description")
  end

  test "trial environments are forced to managed runtime" do
    user = User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")
    organization = Organization.create!(name: "trial-org", plan_tier: Organization::PLAN_TIER_TRIAL)
    ensure_test_organization_runtime!(organization)
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")

    post "/api/v1/cli/projects/#{project.id}/environments",
      params: {
        name: "production",
        runtime_kind: Environment::RUNTIME_CUSTOMER_NODES,
        service_account_email: "custom@example.test"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :unprocessable_entity
    assert_equal "service_account_email is managed by devopsellence", json_body.fetch("error_description")
  end

  test "trial organizations cannot mint manual node bootstrap tokens" do
    user = User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")
    organization = Organization.create!(name: "trial-org", plan_tier: Organization::PLAN_TIER_TRIAL)
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)

    post "/api/v1/cli/organizations/#{organization.id}/node_bootstrap_tokens",
      headers: auth_headers_for(user),
      as: :json

    assert_response :forbidden
    assert_equal "manual node management is unavailable for trial organizations", json_body.fetch("error_description")
  end

  test "publishing a trial release extends the managed lease and returns trial expiry" do
    user = User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")
    organization = Organization.create!(name: "trial-org", plan_tier: Organization::PLAN_TIER_TRIAL)
    ensure_test_organization_runtime!(organization)
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_MANAGED
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rev-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )
    runtime = Devopsellence::RuntimeConfig.current
    node, = issue_test_node!(
      organization: organization,
      name: "trial-node",
      managed: true,
      managed_provider: runtime.managed_default_provider,
      managed_region: runtime.managed_default_region,
      managed_size_slug: runtime.managed_default_size_slug,
      provider_server_id: "srv-1",
      public_ip: "203.0.113.10"
    )
    node.update!(environment: environment, lease_expires_at: 5.minutes.from_now)

    with_object_store(FakeObjectStore.new) do
      EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(true)
      Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
        Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
      )
      perform_enqueued_jobs only: Deployments::PublishJob do
        post "/api/v1/cli/releases/#{release.id}/publish",
          params: { environment_id: environment.id },
          headers: auth_headers_for(user),
          as: :json
      end
    end

    assert_response :created
    assert json_body.fetch("trial_expires_at").present?
    assert node.reload.lease_expires_at > 50.minutes.from_now
  end

  test "trial organizations can delete managed environments" do
    user = User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")
    organization = Organization.create!(name: "trial-org", plan_tier: Organization::PLAN_TIER_TRIAL)
    ensure_test_organization_runtime!(organization)
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_MANAGED
    )
    ensure_test_environment_bundle!(environment)
    runtime = Devopsellence::RuntimeConfig.current
    node, = issue_test_node!(
      organization: organization,
      name: "trial-node",
      managed: true,
      managed_provider: runtime.managed_default_provider,
      managed_region: runtime.managed_default_region,
      managed_size_slug: runtime.managed_default_size_slug,
      provider_server_id: "srv-1",
      public_ip: "203.0.113.10"
    )
    node.update!(environment: environment)

    Nodes::Cleanup.any_instance.stubs(:call).returns(true)
    delete "/api/v1/cli/environments/#{environment.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_equal environment.id, json_body.fetch("id")
    assert_equal [ node.id ], json_body.fetch("managed_node_ids")
    assert_nil Environment.find_by(id: environment.id)
  end

  private

  def auth_headers_for(user)
    _record, access_token, _refresh_token = ApiToken.issue!(user: user)
    { "Authorization" => "Bearer #{access_token}" }
  end

  def json_body
    JSON.parse(response.body)
  end
end
