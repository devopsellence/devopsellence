# frozen_string_literal: true

require "securerandom"
require "test_helper"

class ApiCliIngressStatusTest < ActionDispatch::IntegrationTest
  test "environment status does not advertise ready HTTPS before direct DNS TLS is ready" do
    user, environment, node, hostname = build_ready_direct_dns_environment
    node.update!(ingress_tls_status: Node::INGRESS_TLS_PENDING)

    get "/api/v1/cli/environments/#{environment.id}/status", headers: auth_headers_for(user), as: :json

    assert_response :success
    assert_equal EnvironmentIngress::STATUS_READY, json_body.dig("ingress", "status")
    assert_nil json_body.dig("ingress", "public_url")
    assert_equal [], json_body.dig("ingress", "public_urls")
    assert_equal [ "https://#{hostname}" ], json_body.dig("ingress", "configured_public_urls")
    assert_equal "configured_tls_pending", json_body.dig("ingress", "public_url_status")
    assert_equal Node::INGRESS_TLS_PENDING, json_body.dig("ingress", "tls_status")
  end

  test "environment status advertises HTTPS after direct DNS TLS is ready" do
    user, environment, node, hostname = build_ready_direct_dns_environment
    node.update!(ingress_tls_status: Node::INGRESS_TLS_READY)

    get "/api/v1/cli/environments/#{environment.id}/status", headers: auth_headers_for(user), as: :json

    assert_response :success
    assert_equal EnvironmentIngress::STATUS_READY, json_body.dig("ingress", "status")
    assert_equal "https://#{hostname}", json_body.dig("ingress", "public_url")
    assert_equal [ "https://#{hostname}" ], json_body.dig("ingress", "public_urls")
    assert_equal Node::INGRESS_TLS_READY, json_body.dig("ingress", "tls_status")
    assert_nil json_body.dig("ingress", "public_url_status")
  end

  private

  def build_ready_direct_dns_environment
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    ensure_test_organization_runtime!(organization)

    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "qa",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
      ingress_strategy: Environment::INGRESS_STRATEGY_DIRECT_DNS
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rel-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{"b" * 64}",
      runtime_json: release_runtime_json
    )
    environment.update!(current_release: release)

    node, = issue_test_node!(
      organization: organization,
      name: "node-a",
      public_ip: "198.51.100.10"
    )
    node.update!(environment: environment)
    environment.deployments.create!(
      release: release,
      sequence: 1,
      request_token: SecureRandom.hex(8),
      status: Deployment::STATUS_PUBLISHED,
      status_message: "rollout settled",
      published_at: Time.current,
      finished_at: Time.current
    ).deployment_node_statuses.create!(
      node: node,
      phase: DeploymentNodeStatus::PHASE_SETTLED,
      message: "ok",
      reported_at: Time.current
    )
    hostname = random_ingress_hostname
    environment.create_environment_ingress!(
      hostname: hostname,
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )

    [ user, environment, node, hostname ]
  end

  def auth_headers_for(user)
    _record, access_token, _refresh_token = ApiToken.issue!(user: user)
    { "Authorization" => "Bearer #{access_token}" }
  end

  def json_body
    JSON.parse(response.body)
  end
end
