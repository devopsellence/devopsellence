# frozen_string_literal: true

require "test_helper"
require "json"
require "securerandom"

class ApiAgentBootstrapAndRefreshTest < ActionDispatch::IntegrationTest
  include ActiveJob::TestHelper

  setup do
    clear_enqueued_jobs
    clear_performed_jobs
  end

  test "bootstrap consumes org token and refresh rotates node tokens" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    ensure_test_organization_runtime!(organization)
    _record, raw_bootstrap = NodeBootstrapToken.issue!(organization: organization, issued_by_user: user)
    post "/api/v1/agent/bootstrap", params: { bootstrap_token: raw_bootstrap, name: "node-a" }, as: :json
    assert_response :success
    bootstrap_body = json_body
    assert_equal organization.id, bootstrap_body["organization_id"]
    assert_equal "Bearer", bootstrap_body["token_type"]
    assert_operator bootstrap_body["expires_in"], :>, 0

    node = Node.find(bootstrap_body["node_id"])
    assert_equal organization.id, node.organization_id
    assert_not node.access_token_digest.blank?
    assert_not node.refresh_token_digest.blank?
    assert_nil node.environment_id
    assert_not_nil NodeBootstrapToken.order(:created_at).last.consumed_at

    get "/api/v1/agent/assignment", headers: { "Authorization" => "Bearer #{bootstrap_body["access_token"]}" }, as: :json
    assert_response :success
    assignment_body = json_body
    assert_equal "unassigned", assignment_body["mode"]
    desired_payload = JSON.parse(assignment_body.fetch("desired_state").fetch("payload_json"))
    assert_equal [], desired_payload.fetch("containers")

    original_refresh = bootstrap_body["refresh_token"]
    post "/api/v1/agent/auth/refresh", params: { refresh_token: original_refresh }, as: :json
    assert_response :success
    refresh_body = json_body
    refute_equal bootstrap_body["access_token"], refresh_body["access_token"]
    refute_equal original_refresh, refresh_body["refresh_token"]
    assert_equal "Bearer", refresh_body["token_type"]
  end

  test "refresh returns pointer uri for pointer-capable assigned agents" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(name: "production")

    org_bundle = OrganizationBundle.create!(
      runtime_project: RuntimeProject.default!,
      claimed_by_organization: organization,
      status: OrganizationBundle::STATUS_CLAIMED
    )
    env_bundle = EnvironmentBundle.create!(
      runtime_project: RuntimeProject.default!,
      organization_bundle: org_bundle,
      claimed_by_environment: environment,
      status: EnvironmentBundle::STATUS_CLAIMED
    )
    node_bundle = NodeBundle.create!(
      runtime_project: RuntimeProject.default!,
      organization_bundle: org_bundle,
      environment_bundle: env_bundle,
      status: NodeBundle::STATUS_CLAIMED
    )
    node, _access_token, refresh_token = issue_test_node!(organization: organization, name: "node-a")
    node.update!(
      environment: environment,
      node_bundle: node_bundle,
      desired_state_bucket: "bucket-a",
      desired_state_object_path: "nodes/#{node.id}/desired_state.json",
      desired_state_sequence: 1
    )

    post "/api/v1/agent/auth/refresh",
      params: { refresh_token: refresh_token },
      headers: { "devopsellence-agent-capabilities" => Nodes::DesiredStatePointer::CAPABILITY },
      as: :json

    assert_response :success
    assert_equal "gs://bucket-a/nodes/#{node.id}/desired_state_pointer.json", json_body.dig("desired_state_target", "desired_state_uri")
    assert_equal 1, json_body.dig("desired_state_target", "desired_state_sequence")
  end

  test "bootstrap rejects invalid token" do
    post "/api/v1/agent/bootstrap", params: { bootstrap_token: "bad-token" }, as: :json
    assert_response :unauthorized
    assert_equal "invalid_grant", json_body["error"]
  end

  test "bootstrap auto-assigns node to environment from token" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
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
    environment_bundle = ensure_test_environment_bundle!(environment)
    NodeBundle.create!(
      runtime_project: environment_bundle.runtime_project,
      organization_bundle: environment_bundle.organization_bundle,
      environment_bundle: environment_bundle,
      status: NodeBundle::STATUS_WARM,
      provisioned_at: 1.hour.ago
    )
    _record, raw_bootstrap = NodeBootstrapToken.issue!(organization: organization, environment: environment, issued_by_user: user)

    store = FakeObjectStore.new
    with_object_store(store) do
      with_fake_broker do
        assert_no_enqueued_jobs only: Nodes::BootstrapAssignmentJob do
          post "/api/v1/agent/bootstrap", params: { bootstrap_token: raw_bootstrap, name: "node-a" }, as: :json
        end
      end
    end

    assert_response :success
    node = Node.find(json_body["node_id"])
    assert_equal environment.id, node.environment_id
    assert node.node_bundle.present?
    assert_equal "assigned", json_body.dig("desired_state_target", "mode")
    assert_match %r{\Ags://}, json_body.dig("desired_state_target", "desired_state_uri")
  end

  test "bootstrap queues assignment retry when immediate auto-assignment fails" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(name: "production")
    _record, raw_bootstrap = NodeBootstrapToken.issue!(organization: organization, environment: environment, issued_by_user: user)

    Nodes::AssignmentManager.any_instance.stubs(:call).raises(Nodes::AssignmentManager::Error, "bundle unavailable")

    assert_enqueued_jobs 1, only: Nodes::BootstrapAssignmentJob do
      post "/api/v1/agent/bootstrap", params: { bootstrap_token: raw_bootstrap, name: "node-a" }, as: :json
    end

    assert_response :success
    node = Node.find(json_body["node_id"])
    assert_nil node.environment_id
    assert_nil json_body["desired_state_target"]
  end

  test "bootstrap ignores environment scope and leaves node unassigned" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
    )
    _record, raw_bootstrap = NodeBootstrapToken.issue!(organization: organization, issued_by_user: user)

    post "/api/v1/agent/bootstrap", params: { bootstrap_token: raw_bootstrap, name: "node-b" }, as: :json
    assert_response :success
    body = json_body
    assert_nil body["environment_id"]

    node = Node.find(body["node_id"])
    assert_nil node.environment_id
  end

  test "bootstrap hydrates managed node metadata from managed bootstrap token" do
    record, raw_bootstrap = NodeBootstrapToken.issue!(
      purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
      managed_provider: "hetzner",
      managed_region: "ash",
      managed_size_slug: "cpx11"
    )
    record.update!(provider_server_id: "12345", public_ip: "198.51.100.7")

    post "/api/v1/agent/bootstrap",
      params: { bootstrap_token: raw_bootstrap, provider_server_id: "12345", name: "managed-node-a" },
      as: :json
    assert_response :success

    node = Node.find(json_body["node_id"])
    assert_equal true, node.managed
    assert_equal "hetzner", node.managed_provider
    assert_equal "ash", node.managed_region
    assert_equal "cpx11", node.managed_size_slug
    assert_equal "12345", node.provider_server_id
    assert_equal "198.51.100.7", node.public_ip
    assert_nil node.organization_id
    assert_nil node.node_bundle_id
    assert_nil json_body["desired_state_target"]
    assert_equal node.id, record.reload.node_id
  end

  test "bootstrap rejects managed pool bootstrap with unexpected provider server id" do
    record, raw_bootstrap = NodeBootstrapToken.issue!(
      purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
      managed_provider: "hetzner",
      managed_region: "ash",
      managed_size_slug: "cpx11"
    )
    record.update!(provider_server_id: "12345", public_ip: "198.51.100.7")

    post "/api/v1/agent/bootstrap",
      params: { bootstrap_token: raw_bootstrap, provider_server_id: "99999", name: "managed-node-a" },
      as: :json

    assert_response :unauthorized
    assert_equal "invalid_grant", json_body["error"]
    assert_nil record.reload.node_id
  end

  private

  def json_body
    JSON.parse(response.body)
  end

  def with_fake_broker
    fake_result = Struct.new(:status, :message, keyword_init: true).new(status: :ready, message: nil)
    fake_broker = mock("broker")
    fake_broker.stubs(:ensure_node_bundle_impersonation!).returns(fake_result)
    Runtime::Broker.stubs(:current).returns(fake_broker)
    yield
  end
end
