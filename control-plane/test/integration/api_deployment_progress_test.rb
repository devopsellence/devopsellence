# frozen_string_literal: true

require "json"
require "securerandom"
require "test_helper"

class ApiDeploymentProgressTest < ActionDispatch::IntegrationTest
  include ActiveSupport::Testing::TimeHelpers

  test "agent status updates deployment progress and cli can read it" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rel-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )
    node, access_token, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)
    hostname = random_ingress_hostname
    environment.create_environment_ingress!(
      hostname: hostname,
      cloudflare_tunnel_id: "tunnel-1",
      gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    deployment = Deployments::Publisher.new(environment: environment, release: release, store: FakeObjectStore.new).call.deployment

    post "/api/v1/agent/status",
      params: {
        time: "2026-03-15T12:34:56Z",
        revision: release.revision,
        phase: "reconciling",
        message: "pulling image",
        summary: {
          environments: 1,
          services: 1,
          unhealthy_services: 0
        },
        environments: [
          {
            name: environment.name,
            revision: release.revision,
            phase: "reconciling",
            services: [
              { name: "web", kind: "web", phase: "reconciling", state: "starting", hash: "sha256:#{'c' * 64}" }
            ]
          }
        ]
      },
      headers: { "Authorization" => "Bearer #{access_token}" },
      as: :json

    assert_response :accepted
    assert_equal true, json_body["tracked"]
    assert_equal deployment.id, json_body["deployment_id"]

    get "/api/v1/cli/deployments/#{deployment.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    body = json_body
    assert_equal deployment.id, body["id"]
    assert_equal Deployment::STATUS_ROLLING_OUT, body.fetch("status")
    assert_equal 1, body.dig("summary", "assigned_nodes")
    assert_equal 0, body.dig("summary", "pending")
    assert_equal 1, body.dig("summary", "reconciling")
    assert_equal false, body.dig("summary", "complete")
    assert_equal "reconciling", body.dig("nodes", 0, "phase")
    assert_equal "pulling image", body.dig("nodes", 0, "message")
    assert_equal "node-a", body.dig("nodes", 0, "name")
    assert_equal "starting", body.dig("nodes", 0, "environments", 0, "services", 0, "state")
    assert_equal hostname, body.dig("ingress", "hostname")
    assert_equal "https://#{hostname}", body.dig("ingress", "public_url")
    assert_equal EnvironmentIngress::STATUS_READY, body.dig("ingress", "status")

    post "/api/v1/agent/status",
      params: {
        time: "2026-03-15T12:35:30Z",
        revision: release.revision,
        phase: "settled",
        message: "revision healthy",
        summary: {
          environments: 1,
          services: 1,
          unhealthy_services: 0
        },
        environments: [
          {
            name: environment.name,
            revision: release.revision,
            phase: "settled",
            services: [
              { name: "web", kind: "web", phase: "settled", state: "running", hash: "sha256:#{'c' * 64}" }
            ]
          }
        ]
      },
      headers: { "Authorization" => "Bearer #{access_token}" },
      as: :json

    assert_response :accepted

    get "/api/v1/cli/deployments/#{deployment.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    body = json_body
    assert_equal Deployment::STATUS_PUBLISHED, body.fetch("status")
    assert_equal true, body.dig("summary", "complete")
    assert_equal false, body.dig("summary", "failed")
    assert_equal 1, body.dig("summary", "settled")
    assert_equal "settled", body.dig("nodes", 0, "phase")
    assert_equal "revision healthy", body.dig("nodes", 0, "message")
    assert_equal "rollout settled", body.fetch("status_message")
    assert_equal "https://#{hostname}", body.dig("ingress", "public_url")
  end

  test "repeated settled status does not move finished_at" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rel-repeat",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )
    node, access_token, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)

    deployment = environment.deployments.create!(
      release: release,
      sequence: 1,
      request_token: "req-repeat",
      status: Deployment::STATUS_ROLLING_OUT,
      status_message: "waiting for node reconcile",
      published_at: Time.current
    )
    deployment.deployment_node_statuses.create!(
      node: node,
      phase: DeploymentNodeStatus::PHASE_PENDING,
      message: "waiting for node to reconcile"
    )

    travel_to Time.zone.parse("2026-03-15 12:00:00 UTC") do
      post "/api/v1/agent/status",
        params: {
          time: "2026-03-15T12:00:00Z",
          revision: release.revision,
          phase: "settled",
          message: "created=0 updated=0 removed=0 unchanged=1"
        },
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json

      assert_response :accepted
    end

    deployment.reload
    first_finished_at = deployment.finished_at
    assert_equal Time.zone.parse("2026-03-15 12:00:00 UTC"), first_finished_at
    assert_equal Deployment::STATUS_PUBLISHED, deployment.status

    travel_to Time.zone.parse("2026-03-15 12:05:00 UTC") do
      post "/api/v1/agent/status",
        params: {
          time: "2026-03-15T12:05:00Z",
          revision: release.revision,
          phase: "settled",
          message: "created=0 updated=0 removed=0 unchanged=1"
        },
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json

      assert_response :accepted
    end

    deployment.reload
    assert_equal first_finished_at, deployment.finished_at
    assert_equal "rollout settled", deployment.status_message
  end

  test "deployment progress returns nil ingress when none provisioned" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      runtime_kind: Environment::RUNTIME_MANAGED,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rel-2",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )
    deployment = environment.deployments.create!(
      release: release,
      sequence: 1,
      request_token: "req-2",
      status: Deployment::STATUS_SCHEDULING,
      status_message: "booting managed node",
      published_at: Time.current
    )

    get "/api/v1/cli/deployments/#{deployment.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_nil json_body["ingress"]
  end

  test "transient node errors do not immediately fail deployment progress" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rel-3",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )
    node, access_token, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)

    deployment = environment.deployments.create!(
      release: release,
      sequence: 1,
      request_token: "req-transient",
      status: Deployment::STATUS_PUBLISHED,
      status_message: "waiting for node reconcile",
      published_at: Time.current
    )
    deployment.deployment_node_statuses.create!(
      node: node,
      phase: DeploymentNodeStatus::PHASE_PENDING,
      message: "waiting for node to reconcile"
    )

    travel_to Time.zone.parse("2026-03-15 12:00:00 UTC") do
      post "/api/v1/agent/status",
        params: {
          time: "2026-03-15T12:00:00Z",
          revision: release.revision,
          phase: "error",
          error: "desired state envelope sequence rollback: got 0 want >= 2"
        },
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json

      assert_response :accepted
    end

    get "/api/v1/cli/deployments/#{deployment.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    body = json_body
    assert_equal Deployment::STATUS_ROLLING_OUT, body.fetch("status")
    assert_equal false, body.dig("summary", "failed")
    assert_equal false, body.dig("summary", "complete")
    assert_equal "node reported error, waiting for retry", body.fetch("status_message")
    assert_equal "error", body.dig("nodes", 0, "phase")

    travel_to Time.zone.parse("2026-03-15 12:00:06 UTC") do
      post "/api/v1/agent/status",
        params: {
          time: "2026-03-15T12:00:06Z",
          revision: release.revision,
          phase: "error",
          error: "desired state envelope sequence rollback: got 0 want >= 2"
        },
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json

      assert_response :accepted
    end

    get "/api/v1/cli/deployments/#{deployment.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    body = json_body
    assert_equal Deployment::STATUS_FAILED, body.fetch("status")
    assert_equal true, body.dig("summary", "failed")
    assert_equal "publish failed", body.fetch("status_message")
    assert_equal "desired state envelope sequence rollback: got 0 want >= 2", body.fetch("error_message")
  end

  test "cli can read scheduling deployment progress before node assignment exists" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      runtime_kind: Environment::RUNTIME_MANAGED,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rel-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )
    deployment = environment.deployments.create!(
      release: release,
      sequence: 1,
      request_token: "req-1",
      status: Deployment::STATUS_SCHEDULING,
      status_message: "booting managed node",
      published_at: Time.current
    )

    get "/api/v1/cli/deployments/#{deployment.id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    body = json_body
    assert_equal Deployment::STATUS_SCHEDULING, body.fetch("status")
    assert_equal "booting managed node", body.fetch("status_message")
    assert_equal true, body.dig("summary", "active")
    assert_equal false, body.dig("summary", "complete")
    assert_equal [], body.fetch("nodes")
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
