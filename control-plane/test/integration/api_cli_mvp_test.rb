# frozen_string_literal: true

require "json"
require "securerandom"
require "test_helper"

class ApiCliMvpTest < ActionDispatch::IntegrationTest
  include ActiveJob::TestHelper

  setup do
    clear_enqueued_jobs
    clear_performed_jobs
    ActionMailer::Base.deliveries.clear
  end

  test "starts cli auth by returning a browser login url" do
    assert_no_difference(["User.count", "LoginLink.count"]) do
      post "/api/v1/cli/auth/start",
        params: {
          email: "new-user@example.com",
          redirect_uri: "http://127.0.0.1:45678/callback",
          state: "state-123",
          code_challenge: "challenge-123",
          code_challenge_method: "S256",
          client_id: "cli"
        },
        as: :json
    end

    assert_response :created
    assert_equal "ok", json_body.fetch("status")
    assert_equal "Open the browser login URL and continue with GitHub or Google.", json_body.fetch("message")

    expected_public_base_url = Devopsellence::RuntimeConfig.current.public_base_url.presence || "http://www.example.com"
    expected_login_base_url = URI.parse(expected_public_base_url)
    login_url = URI.parse(json_body.fetch("login_url"))
    params = Rack::Utils.parse_nested_query(login_url.query)

    assert_equal expected_login_base_url.scheme, login_url.scheme
    assert_equal expected_login_base_url.host, login_url.host
    assert_equal "/cli/login", login_url.path
    assert_equal "http://127.0.0.1:45678/callback", params.fetch("redirect_uri")
    assert_equal "state-123", params.fetch("state")
    assert_equal "challenge-123", params.fetch("code_challenge")
    assert_equal "S256", params.fetch("code_challenge_method")
  end

  test "rejects invalid cli auth start redirect uri" do
    assert_no_difference(["User.count", "LoginLink.count"]) do
      post "/api/v1/cli/auth/start",
        params: {
          email: "new-user@example.com",
          redirect_uri: "https://example.com/callback",
          state: "state-123",
          code_challenge: "challenge-123",
          code_challenge_method: "S256",
          client_id: "cli"
        },
        as: :json
    end

    assert_response :bad_request
    assert_equal "invalid redirect_uri", json_body.fetch("error_description")
  end

  test "lists organizations for the authenticated user" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    alpha = Organization.create!(name: "alpha")
    beta = Organization.create!(name: "beta")
    OrganizationMembership.create!(organization: alpha, user: user, role: OrganizationMembership::ROLE_OWNER)
    OrganizationMembership.create!(organization: beta, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)

    get "/api/v1/cli/organizations", headers: auth_headers_for(user), as: :json

    assert_response :success
    assert_equal [
      { "id" => alpha.id, "name" => "alpha", "role" => "owner", "plan_tier" => "paid" },
      { "id" => beta.id, "name" => "beta", "role" => "contributor", "plan_tier" => "paid" }
    ], json_body.fetch("organizations")
  end

  test "creates an organization and owner membership" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)

    with_runtime_config(managed_pool_target: "2") do
      with_successful_organization_runtime_provisioning do
        assert_enqueued_with(job: Runtime::EnsureBundlesJob) do
          assert_difference(["Organization.count", "OrganizationMembership.count"], 1) do
            post "/api/v1/cli/organizations",
              params: { name: "acme" },
              headers: auth_headers_for(user),
              as: :json
          end
        end
      end
    end

    assert_response :created
    assert_equal "acme", json_body.fetch("name")
    assert_equal "owner", json_body.fetch("role")
  end

  test "owner can delete a project through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "alpha")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(name: "production")
    environment_bundle = ensure_test_environment_bundle!(environment)

    assert_difference(["Project.count", "Environment.count", "EnvironmentBundle.count"], -1) do
      delete "/api/v1/cli/projects/#{project.id}",
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :success
    assert_equal true, json_body.fetch("deleted")
    assert_equal "ShopApp", json_body.fetch("name")
    assert_not EnvironmentBundle.exists?(environment_bundle.id)
  end

  test "owner can create a project through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "alpha")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)

    assert_difference("Project.count", 1) do
      post "/api/v1/cli/projects",
        params: { organization_id: organization.id, name: "ShopApp" },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :created
    assert_equal "ShopApp", json_body.fetch("name")
    assert_equal organization.id, json_body.fetch("organization_id")
  end

  test "contributor cannot create a project through the cli api" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "alpha")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)

    assert_no_difference("Project.count") do
      post "/api/v1/cli/projects",
        params: { organization_id: organization.id, name: "ShopApp" },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :forbidden
    assert_equal "owner role required", json_body.fetch("error_description")
  end

  test "lists and revokes cli api tokens" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    current_token, current_access, _current_refresh = ApiToken.issue!(user: user)
    old_token, = ApiToken.issue_ci_token!(user: user, name: "deploy")
    old_token.update!(last_used_at: 5.minutes.ago)

    get "/api/v1/cli/tokens",
      headers: { "Authorization" => "Bearer #{current_access}" },
      as: :json

    assert_response :success
    listed = json_body.fetch("tokens")
    assert_equal [old_token.id, current_token.id], listed.map { |token| token.fetch("id") }
    assert_equal true, listed.first.fetch("current") == false
    assert_equal true, listed.second.fetch("current")

    delete "/api/v1/cli/tokens/#{old_token.id}",
      headers: { "Authorization" => "Bearer #{current_access}" },
      as: :json

    assert_response :success
    assert_equal old_token.id, json_body.fetch("id")
    assert json_body.fetch("revoked_at").present?
    assert_not_nil old_token.reload.revoked_at
  end

  test "deploy target resolves and creates missing organization project and environment" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)

    with_successful_organization_runtime_provisioning do
      Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
        Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
      )
      assert_enqueued_with(job: Runtime::EnsureBundlesJob) do
        post "/api/v1/cli/deploy_target",
          params: {
            organization: "acme",
            project: "ShopApp",
            environment: "production"
          },
          headers: auth_headers_for(user),
          as: :json
      end
    end

    assert_response :created
    assert_equal true, json_body.fetch("organization_created")
    assert_equal true, json_body.fetch("project_created")
    assert_equal true, json_body.fetch("environment_created")
    assert_equal "acme", json_body.dig("organization", "name")
    assert_equal "ShopApp", json_body.dig("project", "name")
    assert_equal "production", json_body.dig("environment", "name")
    assert_equal "managed", json_body.dig("environment", "runtime_kind")
  end

  test "deploy target resolves existing organization by preferred id when organization is omitted" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    alpha = Organization.create!(name: "alpha")
    beta = Organization.create!(name: "beta")
    OrganizationMembership.create!(organization: alpha, user: user, role: OrganizationMembership::ROLE_OWNER)
    OrganizationMembership.create!(organization: beta, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = beta.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: beta.gcp_project_id,
      gcp_project_number: beta.gcp_project_number,
      workload_identity_pool: beta.workload_identity_pool,
      workload_identity_provider: beta.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_MANAGED
    )

    post "/api/v1/cli/deploy_target",
      params: {
        preferred_organization_id: beta.id,
        project: "ShopApp",
        environment: "production"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_equal false, json_body.fetch("organization_created")
    assert_equal false, json_body.fetch("project_created")
    assert_equal false, json_body.fetch("environment_created")
    assert_equal beta.id, json_body.dig("organization", "id")
    assert_equal project.id, json_body.dig("project", "id")
    assert_equal environment.id, json_body.dig("environment", "id")
  end

  test "deploy target prefers the default organization when organization is omitted" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    beta = Organization.create!(name: "beta")
    default = Organization.create!(name: Organization::DEFAULT_NAME)
    OrganizationMembership.create!(organization: beta, user: user, role: OrganizationMembership::ROLE_OWNER)
    OrganizationMembership.create!(organization: default, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = default.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: default.gcp_project_id,
      gcp_project_number: default.gcp_project_number,
      workload_identity_pool: default.workload_identity_pool,
      workload_identity_provider: default.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_MANAGED
    )

    assert_no_enqueued_jobs only: Runtime::EnsureBundlesJob do
      post "/api/v1/cli/deploy_target",
        params: {
          project: "ShopApp",
          environment: "production"
        },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :success
    assert_equal default.id, json_body.dig("organization", "id")
    assert_equal project.id, json_body.dig("project", "id")
    assert_equal environment.id, json_body.dig("environment", "id")
  end

  test "deploy target sticks to the earliest matching project and environment when duplicate names already exist" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    ensure_test_organization_runtime!(organization)
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)

    first_project = organization.projects.create!(name: "demo")
    second_project = organization.projects.new(name: "demo")
    second_project.save!(validate: false)

    first_environment = first_project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_MANAGED
    )
    duplicate_environment = second_project.environments.new(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_MANAGED
    )
    duplicate_environment.save!(validate: false)

    post "/api/v1/cli/deploy_target",
      params: {
        organization: organization.name,
        project: "demo",
        environment: "production"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_equal first_project.id, json_body.dig("project", "id")
    assert_equal first_environment.id, json_body.dig("environment", "id")
  end

  test "deploy target rejects omitted organization when multiple organizations are available without preference" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    ["alpha", "beta"].each do |name|
      organization = Organization.create!(name: name)
      OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    end

    post "/api/v1/cli/deploy_target",
      params: {
        project: "ShopApp",
        environment: "production"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :unprocessable_entity
    assert_equal "multiple organizations available; pass organization or preferred_organization_id", json_body.fetch("error_description")
  end

  test "contributor can resolve an existing deploy target" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_MANAGED
    )

    post "/api/v1/cli/deploy_target",
      params: {
        organization: organization.name,
        project: "ShopApp",
        environment: "production"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_equal false, json_body.fetch("organization_created")
    assert_equal false, json_body.fetch("project_created")
    assert_equal false, json_body.fetch("environment_created")
    assert_equal project.id, json_body.dig("project", "id")
    assert_equal environment.id, json_body.dig("environment", "id")
  end

  test "contributor cannot create a project through deploy target" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    ensure_test_organization_runtime!(organization)
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)

    assert_no_difference([ "Project.count", "Environment.count" ]) do
      post "/api/v1/cli/deploy_target",
        params: {
          organization: organization.name,
          project: "ShopApp",
          environment: "production"
        },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :forbidden
    assert_equal "owner role required", json_body.fetch("error_description")
  end

  test "contributor cannot create an environment through deploy target" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    ensure_test_organization_runtime!(organization)
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)
    project = organization.projects.create!(name: "ShopApp")

    assert_no_difference("Environment.count") do
      post "/api/v1/cli/deploy_target",
        params: {
          organization: organization.name,
          project: "ShopApp",
          environment: "production"
        },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :forbidden
    assert_equal "owner role required", json_body.fetch("error_description")
  end

  test "returns GAR push auth for an accessible project" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(
      name: "acme",
      gcp_project_id: "runtime-proj",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      gar_repository_region: "us-east1",
      gar_repository_name: "org-1-apps",
      gcs_bucket_name: "devopsellence-acme"
    )
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")

    fake_broker = mock("broker")
    fake_broker.stubs(:issue_gar_push_auth!).returns(
      Runtime::Broker::LocalClient::PushAuth.new(
        registry_host: organization.gar_repository_path.split("/").first,
        gar_repository_path: organization.gar_repository_path,
        docker_username: "oauth2accesstoken",
        docker_password: "ya29.fake",
        expires_in: 1200
      )
    )
    Runtime::Broker.stubs(:current).returns(fake_broker)

    post "/api/v1/cli/projects/#{project.id}/gar/push_auth",
      params: { image_repository: "shop-app" },
      headers: auth_headers_for(user),
      as: :json

    assert_response :created
    assert_equal "us-east1-docker.pkg.dev", json_body.fetch("registry_host")
    assert_equal "us-east1-docker.pkg.dev/runtime-proj/org-1-apps", json_body.fetch("gar_repository_path")
    assert_equal "shop-app", json_body.fetch("image_repository")
    assert_equal "ya29.fake", json_body.fetch("access_token")
  end

  test "returns environment status summary" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(
      name: "acme",
      gcp_project_id: "runtime-proj",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      gar_repository_region: "us-east1",
      gar_repository_name: "org-1-apps",
      gcs_bucket_name: "devopsellence-acme"
    )
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rev-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      runtime_json: release_runtime_json,
      status: Release::STATUS_PUBLISHED,
      published_at: Time.current
    )
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@runtime-proj.iam.gserviceaccount.com",
      current_release: release
    )
    environment.deployments.create!(
      release: release,
      sequence: 1,
      request_token: SecureRandom.hex(8),
      status: Deployment::STATUS_PUBLISHED,
      status_message: "rollout settled",
      published_at: Time.current
    )
    hostname = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"
    environment.create_environment_ingress!(
      hostname: hostname,
      cloudflare_tunnel_id: "tunnel-1",
      gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment, desired_state_sequence: 1)

    get "/api/v1/cli/environments/#{environment.id}/status", headers: auth_headers_for(user), as: :json

    assert_response :success
    assert_equal "acme", json_body.dig("organization", "name")
    assert_equal "ShopApp", json_body.dig("project", "name")
    assert_equal "production", json_body.dig("environment", "name")
    assert_equal release.id, json_body.dig("current_release", "id")
    assert_equal release.image_reference_for(organization), json_body.dig("current_release", "image_reference")
    assert_equal 1, json_body.dig("latest_deployment", "sequence")
    assert_equal 1, json_body.fetch("assigned_nodes")
    assert_nil json_body["warning"]
    assert_equal hostname, json_body.dig("ingress", "hostname")
    assert_equal "https://#{hostname}", json_body.dig("ingress", "public_url")
  end

  test "environment status warns when customer-managed environment has no assigned nodes" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )

    get "/api/v1/cli/environments/#{environment.id}/status", headers: auth_headers_for(user), as: :json

    assert_response :success
    assert_equal 0, json_body.fetch("assigned_nodes")
    assert_includes json_body.fetch("warning"), "devopsellence node register"
  end

  test "publish returns ingress details" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rev-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      runtime_json: release_runtime_json
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)
    hostname = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"
    ingress = environment.create_environment_ingress!(
      hostname: hostname,
      cloudflare_tunnel_id: "tunnel-1",
      gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )

    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(ingress)
    assert_enqueued_jobs 1, only: Deployments::PublishJob do
      post "/api/v1/cli/releases/#{release.id}/publish",
        params: { environment_id: environment.id, request_token: "req-123" },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :created
    assert_equal Deployment::STATUS_SCHEDULING, json_body.fetch("status")
    assert_equal "waiting to publish desired state", json_body.fetch("status_message")
    assert_equal 1, json_body.fetch("assigned_nodes")
    assert_nil json_body["warning"]
    assert_equal hostname, json_body.dig("ingress", "hostname")
    assert_equal "https://#{hostname}", json_body.dig("ingress", "public_url")
    assert_equal EnvironmentIngress::STATUS_READY, json_body.dig("ingress", "status")
    assert_equal hostname, json_body.fetch("hostname")
    assert_equal "https://#{hostname}", json_body.fetch("public_url")
    assert_equal EnvironmentIngress::STATUS_READY, json_body.fetch("ingress_status")
  end

  test "publish warns when customer-managed environment has no assigned nodes" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rev-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      runtime_json: release_runtime_json
    )

    assert_enqueued_jobs 1, only: Deployments::PublishJob do
      post "/api/v1/cli/releases/#{release.id}/publish",
        params: { environment_id: environment.id, request_token: "req-no-nodes" },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :created
    assert_equal 0, json_body.fetch("assigned_nodes")
    assert_includes json_body.fetch("warning"), "devopsellence node register"
  end

  test "publish is idempotent for the same request token" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rev-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      runtime_json: release_runtime_json
    )

    assert_enqueued_jobs 1, only: Deployments::PublishJob do
      2.times do
        post "/api/v1/cli/releases/#{release.id}/publish",
          params: { environment_id: environment.id, request_token: "same-token" },
          headers: auth_headers_for(user),
          as: :json
        assert_response :created
      end
    end

    assert_equal 1, environment.deployments.where(request_token: "same-token").count
  end

  test "lists nodes for an accessible organization" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: "runtime-proj",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      service_account_email: "env@runtime-proj.iam.gserviceaccount.com"
    )
    node, _access, _refresh = issue_test_node!(
      organization: organization,
      name: "node-a",
      labels: ["web", "worker"],
      managed: true,
      managed_provider: "hetzner",
      managed_region: "ash",
      managed_size_slug: "cpx11",
      provider_server_id: "srv-1",
      public_ip: "198.51.100.10"
    )
    node.update!(environment: environment, desired_state_sequence: 1)

    get "/api/v1/cli/organizations/#{organization.id}/nodes", headers: auth_headers_for(user), as: :json

    assert_response :success
    listed = json_body.fetch("nodes").first
    assert_equal node.id, listed.fetch("id")
    assert_equal %w[web worker], listed.fetch("labels")
    assert_equal true, listed.fetch("managed")
    assert_equal "198.51.100.10", listed.fetch("public_ip")
    assert_equal "production", listed.dig("environment", "name")
    assert_nil listed["revoked_at"]
  end

  test "creates managed environment by default" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    post "/api/v1/cli/projects/#{project.id}/environments",
      params: { name: "production" },
      headers: auth_headers_for(user),
      as: :json

    assert_response :created
    assert_equal "managed", json_body.fetch("runtime_kind")
  end

  test "contributor cannot create an environment through the cli api" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")

    assert_no_difference("Environment.count") do
      post "/api/v1/cli/projects/#{project.id}/environments",
        params: { name: "production" },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :forbidden
    assert_equal "owner role required", json_body.fetch("error_description")
  end

  test "rejects switching to direct_dns when assigned web nodes lack capability" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    node, = issue_test_node!(organization: organization, name: "node-a", labels: ["web"])
    node.update!(environment: environment)
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rev-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      runtime_json: release_runtime_json,
      status: Release::STATUS_PUBLISHED,
      published_at: Time.current
    )
    environment.update!(current_release: release)

    patch "/api/v1/cli/environments/#{environment.id}/ingress",
      params: { ingress_strategy: Environment::INGRESS_STRATEGY_DIRECT_DNS },
      headers: auth_headers_for(user),
      as: :json

    assert_response :unprocessable_entity
    assert_match "assigned ingress nodes do not support direct_dns ingress: node-a", json_body.fetch("error_description")
  end

  test "switching ingress enqueues desired state republish for deployed environments" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)
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
      image_digest: "sha256:#{"b" * 64}",
      runtime_json: release_runtime_json
    )
    environment.update!(current_release: release)
    node, = issue_test_node!(organization: organization, name: "node-a", labels: ["web"])
    node.update!(environment: environment)
    node.capabilities = [Node::CAPABILITY_DIRECT_DNS_INGRESS]
    node.save!

    assert_enqueued_with(job: Environments::RepublishDesiredStateJob, args: [environment.id]) do
      assert_enqueued_with(job: EnvironmentIngresses::ReconcileJob, args: [environment.id]) do
        patch "/api/v1/cli/environments/#{environment.id}/ingress",
          params: { ingress_strategy: Environment::INGRESS_STRATEGY_DIRECT_DNS },
          headers: auth_headers_for(user),
          as: :json
      end
    end

    assert_response :success
    assert_equal Environment::INGRESS_STRATEGY_DIRECT_DNS, json_body.fetch("ingress_strategy")
  end

  test "owner can delete an environment through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
    )
    environment_bundle = ensure_test_environment_bundle!(environment)

    customer_bundle = NodeBundle.create!(
      runtime_project: environment_bundle.runtime_project,
      organization_bundle: environment_bundle.organization_bundle,
      environment_bundle: environment_bundle,
      status: NodeBundle::STATUS_CLAIMED
    )
    customer_node, = issue_test_node!(organization: organization, name: "customer-node")
    customer_node.update!(
      environment: environment,
      node_bundle: customer_bundle,
      desired_state_bucket: customer_bundle.desired_state_bucket,
      desired_state_object_path: customer_bundle.desired_state_object_path
    )
    customer_bundle.update!(node: customer_node)

    managed_bundle = NodeBundle.create!(
      runtime_project: environment_bundle.runtime_project,
      organization_bundle: environment_bundle.organization_bundle,
      environment_bundle: environment_bundle,
      status: NodeBundle::STATUS_CLAIMED
    )
    managed_node, = issue_test_node!(
      organization: organization,
      name: "managed-node",
      managed: true,
      managed_provider: "hetzner",
      managed_region: "ash",
      managed_size_slug: "cpx11",
      provider_server_id: "server-123"
    )
    managed_node.update!(
      environment: environment,
      node_bundle: managed_bundle,
      desired_state_bucket: managed_bundle.desired_state_bucket,
      desired_state_object_path: managed_bundle.desired_state_object_path
    )
    managed_bundle.update!(node: managed_node)

    fake_broker = mock("broker")
    fake_broker.stubs(:revoke_node_bundle_impersonation!).returns(
      Runtime::Broker::LocalClient::Result.new(status: :ready, message: nil)
    )
    Runtime::Broker.stubs(:current).returns(fake_broker)

    assert_enqueued_with(job: ManagedNodes::DeleteJob, args: [{ node_id: managed_node.id }]) do
      delete "/api/v1/cli/environments/#{environment.id}",
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :success
    assert_equal environment.id, json_body.fetch("id")
    assert_equal [customer_node.id], json_body.fetch("customer_node_ids")
    assert_equal [managed_node.id], json_body.fetch("managed_node_ids")
    assert_not Environment.exists?(environment.id)
    assert_not EnvironmentBundle.exists?(environment_bundle.id)
    assert_nil customer_node.reload.environment_id
    assert_nil customer_node.node_bundle_id
    assert_equal "", customer_node.desired_state_bucket
    assert_equal "", customer_node.desired_state_object_path
    assert_nil managed_node.reload.environment_id
    assert_nil managed_node.node_bundle_id
  end

  test "contributor cannot delete an environment through the cli api" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(name: "production")

    delete "/api/v1/cli/environments/#{environment.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :forbidden
    assert Environment.exists?(environment.id)
  end

  test "owner can mint node bootstrap token install command" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    with_env(
      "DEVOPSELLENCE_AGENT_CONTAINER_IMAGE" => nil,
      "DEVOPSELLENCE_AGENT_CONTAINER_REPOSITORY" => "us-east1-docker.pkg.dev/devopsellence/agents/devopsellence-agent",
      "DEVOPSELLENCE_STABLE_VERSION" => "v1.2.3"
    ) do
      post "/api/v1/cli/organizations/#{organization.id}/node_bootstrap_tokens",
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :created
    assert_nil json_body["environment"]
    assert_equal "unassigned", json_body.fetch("assignment_mode")
    assert_match %r{/install\.sh \| bash -s -- --token}, json_body.fetch("install_command")
    assert_equal "us-east1-docker.pkg.dev/devopsellence/agents/devopsellence-agent:v1.2.3", json_body.dig("agent_image", "reference")
    assert_equal "v1.2.3", json_body.dig("agent_image", "version")
    assert_equal organization.id, NodeBootstrapToken.order(:created_at).last.organization_id
  end

  test "owner can mint node bootstrap token scoped to an environment" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(name: "production")

    post "/api/v1/cli/organizations/#{organization.id}/node_bootstrap_tokens",
      params: { environment_id: environment.id },
      headers: auth_headers_for(user),
      as: :json

    assert_response :created
    token = NodeBootstrapToken.order(:created_at).last
    assert_equal organization.id, token.organization_id
    assert_equal environment.id, token.environment_id
    assert_equal "environment", json_body.fetch("assignment_mode")
  end

  test "owner cannot scope bootstrap token to environment from another organization" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    other_organization = Organization.create!(name: "other")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    other_project = other_organization.projects.create!(name: "OtherApp")
    other_environment = other_project.environments.create!(name: "production")

    assert_no_difference "NodeBootstrapToken.count" do
      post "/api/v1/cli/organizations/#{organization.id}/node_bootstrap_tokens",
        params: { environment_id: other_environment.id },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :not_found
    assert_equal "environment not found", json_body.fetch("error_description")
  end

  test "contributor cannot mint node bootstrap token" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)

    post "/api/v1/cli/organizations/#{organization.id}/node_bootstrap_tokens",
      headers: auth_headers_for(user),
      as: :json

    assert_response :forbidden
  end

  test "owner can update node labels through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    node, = issue_test_node!(organization: organization, name: "node-a")

    post "/api/v1/cli/nodes/#{node.id}/labels",
      params: { labels: "web,worker" },
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_equal %w[web worker], node.reload.labels
  end

  test "node delete rejects assigned customer-managed nodes through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "development",
      gcp_project_id: "runtime-proj",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      service_account_email: "env@runtime-proj.iam.gserviceaccount.com"
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rel-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      runtime_json: release_runtime_json,
      status: Release::STATUS_PUBLISHED,
      published_at: Time.current
    )
    environment.update!(current_release: release)
    node, = issue_test_node!(organization: organization, name: "dev-laptop")
    node.update!(environment: environment, desired_state_sequence: 3)

    freeze_time do
      delete "/api/v1/cli/nodes/#{node.id}",
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :unprocessable_entity
    assert_equal "node remove requires an unassigned node; use node detach first", json_body.fetch("error_description")
    assert_equal environment.id, node.reload.environment_id
    assert_nil node.revoked_at
  end

  test "node delete rejects assigned managed nodes through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "development",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
    )
    node, = issue_test_node!(
      organization: organization,
      name: "managed-node",
      managed: true,
      managed_provider: "hetzner",
      managed_region: "ash",
      managed_size_slug: "cpx11",
      provider_server_id: "server-123"
    )
    node.update!(environment: environment)

    delete "/api/v1/cli/nodes/#{node.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :unprocessable_entity
    assert_equal "node remove requires an unassigned node; use node detach first", json_body.fetch("error_description")
    assert_equal environment.id, node.reload.environment_id
    assert_nil node.revoked_at
  end

  test "node delete retires an unassigned customer-managed node through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)
    node, = issue_test_node!(organization: organization, name: "dev-laptop")
    fake_broker = mock("broker")
    fake_broker.stubs(:revoke_node_bundle_impersonation!).returns(
      Runtime::Broker::LocalClient::Result.new(status: :ready, message: nil)
    )
    Runtime::Broker.stubs(:current).returns(fake_broker)

    delete "/api/v1/cli/nodes/#{node.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_equal node.id, json_body.fetch("id")
    assert_equal false, json_body.fetch("managed")
    assert_nil json_body["environment_id"]
    assert_not_nil json_body.fetch("revoked_at")
    assert_not Node.exists?(node.id)
  end

  test "node delete retires an unassigned managed node through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)
    node, = issue_test_node!(
      organization: organization,
      name: "managed-node",
      managed: true,
      managed_provider: "hetzner",
      managed_region: "ash",
      managed_size_slug: "cpx11",
      provider_server_id: "server-123"
    )
    fake_broker = mock("broker")
    fake_broker.stubs(:revoke_node_bundle_impersonation!).returns(
      Runtime::Broker::LocalClient::Result.new(status: :ready, message: nil)
    )
    Runtime::Broker.stubs(:current).returns(fake_broker)

    assert_enqueued_with(job: ManagedNodes::DeleteJob, args: [{ node_id: node.id }]) do
      delete "/api/v1/cli/nodes/#{node.id}",
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :success
    assert_equal node.id, json_body.fetch("id")
    assert_equal true, json_body.fetch("managed")
    assert_nil json_body["environment_id"]
    assert_not_nil json_body.fetch("revoked_at")
    assert_not_nil node.reload.revoked_at
  end

  test "owner can unassign a node through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "development",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
    )
    environment_bundle = ensure_test_environment_bundle!(environment)
    node_bundle = NodeBundle.create!(
      runtime_project: environment_bundle.runtime_project,
      organization_bundle: environment_bundle.organization_bundle,
      environment_bundle: environment_bundle,
      status: NodeBundle::STATUS_CLAIMED
    )
    node, = issue_test_node!(organization: organization, name: "dev-laptop")
    node.update!(
      environment: environment,
      node_bundle: node_bundle,
      desired_state_bucket: node_bundle.desired_state_bucket,
      desired_state_object_path: node_bundle.desired_state_object_path,
      desired_state_sequence: 3
    )
    node_bundle.update!(node: node)
    fake_broker = mock("broker")
    fake_broker.stubs(:revoke_node_bundle_impersonation!).returns(
      Runtime::Broker::LocalClient::Result.new(status: :ready, message: nil)
    )
    Runtime::Broker.stubs(:current).returns(fake_broker)

    delete "/api/v1/cli/nodes/#{node.id}/assignment",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_equal node.id, json_body.fetch("id")
    assert_equal environment.id, json_body.fetch("environment_id")
    assert_nil json_body["desired_state_uri"]
    assert_nil node.reload.environment_id
    assert_nil node.node_bundle_id
    assert_nil node.revoked_at
    assert node.access_active?
    assert node.refresh_active?
  end

  test "owner can unassign a managed node through the cli api and schedule delete" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "development",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
    )
    environment_bundle = ensure_test_environment_bundle!(environment)
    node_bundle = NodeBundle.create!(
      runtime_project: environment_bundle.runtime_project,
      organization_bundle: environment_bundle.organization_bundle,
      environment_bundle: environment_bundle,
      status: NodeBundle::STATUS_CLAIMED
    )
    node, = issue_test_node!(
      organization: organization,
      name: "managed-node",
      managed: true,
      managed_provider: "hetzner",
      managed_region: "ash",
      managed_size_slug: "cpx11",
      provider_server_id: "server-123"
    )
    node.update!(
      environment: environment,
      node_bundle: node_bundle,
      desired_state_bucket: node_bundle.desired_state_bucket,
      desired_state_object_path: node_bundle.desired_state_object_path
    )
    node_bundle.update!(node: node)
    fake_broker = mock("broker")
    fake_broker.stubs(:revoke_node_bundle_impersonation!).returns(
      Runtime::Broker::LocalClient::Result.new(status: :ready, message: nil)
    )
    Runtime::Broker.stubs(:current).returns(fake_broker)

    assert_enqueued_with(job: ManagedNodes::DeleteJob, args: [{ node_id: node.id }]) do
      delete "/api/v1/cli/nodes/#{node.id}/assignment",
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :success
    assert_equal node.id, json_body.fetch("id")
    assert_equal environment.id, json_body.fetch("environment_id")
    assert_equal true, json_body.fetch("managed")
    assert_not_nil json_body.fetch("revoked_at")
    assert_nil node.reload.environment_id
    assert_not_nil node.revoked_at
  end

  test "contributor cannot unassign a node through the cli api" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)
    node, = issue_test_node!(organization: organization, name: "dev-laptop")

    delete "/api/v1/cli/nodes/#{node.id}/assignment",
      headers: auth_headers_for(user),
      as: :json

    assert_response :forbidden
    assert_nil node.reload.environment_id
    assert_nil node.node_bundle_id
    assert_nil node.revoked_at
  end

  test "revoked node cannot be assigned through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "development",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
    )
    node, = issue_test_node!(organization: organization, name: "dev-laptop")
    node.update!(revoked_at: Time.current)

    post "/api/v1/cli/environments/#{environment.id}/assignments",
      params: { node_id: node.id },
      headers: auth_headers_for(user),
      as: :json

    assert_response :unprocessable_entity
    assert_equal "node has been deleted; bootstrap again to reuse this machine", json_body.fetch("error_description")
  end

  test "contributor cannot assign a node through the cli api" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "development",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
    )
    node, = issue_test_node!(organization: organization, name: "dev-laptop")

    post "/api/v1/cli/environments/#{environment.id}/assignments",
      params: { node_id: node.id },
      headers: auth_headers_for(user),
      as: :json

    assert_response :forbidden
    assert_equal "owner role required", json_body.fetch("error_description")
  end

  test "contributor cannot cleanup a node through the cli api" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)
    node, = issue_test_node!(organization: organization, name: "dev-laptop")

    delete "/api/v1/cli/nodes/#{node.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :forbidden
    assert_nil node.reload.revoked_at
  end

  test "creates an environment secret through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: "runtime-proj",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      service_account_email: "env@runtime-proj.iam.gserviceaccount.com"
    )

    Gcp::EnvironmentSecretManager.any_instance.stubs(:upsert!)
      .with do |environment_secret:, value:|
        raise ArgumentError, "secret value is required" if value.blank?

        environment_secret.save!
        true
      end
      .returns(true)
    post "/api/v1/cli/environments/#{environment.id}/secrets",
      params: {
        service_name: "web",
        name: "SECRET_KEY_BASE",
        value: "super-secret"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :created
    assert_equal "web", json_body.fetch("service_name")
    assert_equal "SECRET_KEY_BASE", json_body.fetch("name")
    assert_match %r{\Agsm://}, json_body.fetch("secret_ref")
  end

  test "lists environment secrets through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: "runtime-proj",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      service_account_email: "env@runtime-proj.iam.gserviceaccount.com"
    )
    environment.environment_secrets.create!(service_name: "worker", name: "REDIS_URL")
    environment.environment_secrets.create!(service_name: "web", name: "SECRET_KEY_BASE")

    get "/api/v1/cli/environments/#{environment.id}/secrets",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_equal ["web", "worker"], json_body.fetch("secrets").map { |secret| secret.fetch("service_name") }
    assert_equal ["SECRET_KEY_BASE", "REDIS_URL"], json_body.fetch("secrets").map { |secret| secret.fetch("name") }
  end

  test "deletes an environment secret through the cli api" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: "runtime-proj",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      service_account_email: "env@runtime-proj.iam.gserviceaccount.com"
    )
    secret = environment.environment_secrets.create!(service_name: "web", name: "SECRET_KEY_BASE")

    Gcp::EnvironmentSecretManager.any_instance.stubs(:destroy!)
      .with do |environment_secret:|
        environment_secret.destroy!
        true
      end
      .returns(true)
    delete "/api/v1/cli/environments/#{environment.id}/secrets/web/SECRET_KEY_BASE",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_equal secret.id, json_body.fetch("id")
    assert_not EnvironmentSecret.exists?(secret.id)
  end

  test "creates a release with secret refs from json api arrays" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")

    post "/api/v1/cli/projects/#{project.id}/releases",
      params: {
        git_sha: "a" * 40,
        image_repository: "shop-app",
        image_digest: "sha256:#{'b' * 64}",
        services: {
          web: web_service_runtime(
            port: 80,
            secret_refs: [
              {
                name: "SECRET_KEY_BASE",
                secret: "gsm://projects/runtime-dev-example/secrets/smoke-app-secret-key-base/versions/latest"
              }
            ]
          )
        },
        ingress_service: "web"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :created
    release = project.releases.order(:id).last
    assert_equal [
      {
        "name" => "SECRET_KEY_BASE",
        "secret" => "gsm://projects/runtime-dev-example/secrets/smoke-app-secret-key-base/versions/latest"
      }
    ], JSON.parse(release.runtime_json).dig("services", "web", "secret_refs")
  end

  test "contributor cannot create a release through the cli api" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)
    project = organization.projects.create!(name: "ShopApp")

    assert_no_difference("Release.count") do
      post "/api/v1/cli/projects/#{project.id}/releases",
        params: {
          git_sha: "a" * 40,
          image_repository: "shop-app",
          image_digest: "sha256:#{'b' * 64}",
          services: { web: web_service_runtime(port: 80) },
          ingress_service: "web"
        },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :forbidden
    assert_equal "owner role required", json_body.fetch("error_description")
  end

  test "contributor cannot publish a release through the cli api" do
    user = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_CONTRIBUTOR)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rev-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      runtime_json: release_runtime_json
    )

    assert_no_difference("Deployment.count") do
      post "/api/v1/cli/releases/#{release.id}/publish",
        params: { environment_id: environment.id, request_token: "req-contrib" },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :forbidden
    assert_equal "owner role required", json_body.fetch("error_description")
  end

  test "creates a release with web and worker runtime config" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")

    post "/api/v1/cli/projects/#{project.id}/releases",
      params: {
        git_sha: "a" * 40,
        image_repository: "shop-app",
        image_digest: "sha256:#{'b' * 64}",
        services: {
          web: web_service_runtime(
            port: 80,
            env: { "RAILS_ENV" => "production" },
            volumes: [{ source: "app_storage", target: "/rails/storage" }]
          ),
          worker: worker_service_runtime(
            command: ["./bin/jobs"],
            volumes: [{ source: "app_storage", target: "/rails/storage" }]
          )
        },
        ingress_service: "web"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :created
    release = project.releases.order(:id).last
    runtime = JSON.parse(release.runtime_json)
    assert_equal ["./bin/jobs"], runtime.dig("services", "worker", "command")
    assert_equal 80, runtime.dig("services", "web", "ports").first.fetch("port")
    assert_equal 80, runtime.dig("services", "web", "healthcheck", "port")
  end

  test "creates a web-only structured release" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")

    post "/api/v1/cli/projects/#{project.id}/releases",
      params: {
        git_sha: "a" * 40,
        image_repository: "shop-app",
        image_digest: "sha256:#{'b' * 64}",
        services: {
          web: web_service_runtime(port: 80, env: { "RAILS_ENV" => "production" })
        },
        ingress_service: "web"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :created
    release = project.releases.order(:id).last
    runtime = JSON.parse(release.runtime_json)
    assert_equal 80, runtime.dig("services", "web", "ports").first.fetch("port")
    assert_not runtime.fetch("services").key?("worker")
    assert_equal false, release.requires_label?("worker")
  end

  test "rejects release create without web runtime config" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")

    post "/api/v1/cli/projects/#{project.id}/releases",
      params: {
        git_sha: "a" * 40,
        image_repository: "shop-app",
        image_digest: "sha256:#{'b' * 64}",
        services: {}
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :unprocessable_entity
    assert_equal "invalid_request", json_body.fetch("error")
    assert_equal "services is required", json_body.fetch("error_description")
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
