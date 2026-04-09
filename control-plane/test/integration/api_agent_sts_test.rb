# frozen_string_literal: true

require "base64"
require "json"
require "openssl"
require "test_helper"
require "securerandom"

class ApiAgentStsTest < ActionDispatch::IntegrationTest
  POOL_A = "projects/123456789/locations/global/workloadIdentityPools/pool-a"
  PROVIDER_A = "#{POOL_A}/providers/provider-a"

  test "returns subject token for provisioned assigned node" do
    access_token, organization, environment, node = setup_assigned_node

    with_rsa_signing_key do
      post "/api/v1/agent/sts/token",
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json
    end

    assert_response :success
    body = json_body
    assert_equal environment.audience, body["audience"]
    assert_equal "urn:ietf:params:oauth:token-type:jwt", body["subject_token_type"]
    assert_operator body["expires_in"], :>, 0

    claims = decode_jwt_payload(body["subject_token"])
    assert_equal "node:#{node.id}", claims["sub"]
    assert_equal environment.project_id.to_s, claims["project_id"]
    assert_equal environment.id.to_s, claims["environment_id"]
    assert_equal environment.identity_version.to_s, claims["identity_version"]
    assert_equal node.organization_id.to_s, claims["organization_id"]
    assert_equal environment.service_account_email, claims["service_account_email"]
  end

  test "rejects invalid access token" do
    post "/api/v1/agent/sts/token",
      headers: { "Authorization" => "Bearer bad-token" },
      as: :json

    assert_response :unauthorized
    assert_equal "invalid_grant", json_body["error"]
  end

  test "rejects provisioned unassigned node" do
    access_token, organization, _environment, node = setup_unassigned_node

    with_rsa_signing_key do
      post "/api/v1/agent/sts/token",
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json
    end

    assert_response :forbidden
    assert_equal "invalid_target", json_body["error"]
  end

  test "returns subject token for managed warm node without environment assignment" do
    runtime = RuntimeProject.default!
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-ob-#{SecureRandom.hex(3)}",
      gar_repository_name: "ob-#{SecureRandom.hex(3)}-apps",
      gar_repository_region: runtime.gar_region,
      gar_writer_service_account_email: "ob#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_WARM
    )
    environment_bundle = EnvironmentBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      service_account_email: "warm-node@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      gcp_secret_name: "eb-#{SecureRandom.hex(4)}-secret",
      status: EnvironmentBundle::STATUS_WARM
    )
    node, access_token, _refresh_token = issue_test_node!(
      organization: nil,
      name: "managed-node-1",
      managed: true,
      managed_provider: "hetzner",
      managed_region: "ash",
      managed_size_slug: "cpx11",
      provider_server_id: "srv-123"
    )
    node_bundle = NodeBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      environment_bundle: environment_bundle,
      node: node,
      status: NodeBundle::STATUS_WARM
    )
    node.update!(
      node_bundle: node_bundle,
      desired_state_bucket: organization_bundle.gcs_bucket_name,
      desired_state_object_path: node_bundle.desired_state_object_path
    )

    with_rsa_signing_key do
      post "/api/v1/agent/sts/token",
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json
    end

    assert_response :success
    body = json_body
    assert_equal environment_bundle.audience, body["audience"]

    claims = decode_jwt_payload(body["subject_token"])
    assert_equal organization_bundle.token, claims["organization_bundle_token"]
    assert_equal environment_bundle.token, claims["environment_bundle_token"]
    assert_equal node_bundle.token, claims["node_bundle_token"]
    assert_equal environment_bundle.service_account_email, claims["service_account_email"]
  end

  test "rejects assigned node without environment runtime identity" do
    access_token, _organization, environment, _node = setup_assigned_node
    environment.update_column(:service_account_email, "")

    with_rsa_signing_key do
      post "/api/v1/agent/sts/token",
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json
    end

    assert_response :forbidden
    assert_equal "invalid_target", json_body["error"]
  end

  test "returns service unavailable when idp signing key missing" do
    access_token, _organization, _environment, _node = setup_assigned_node

    with_env("DEVOPSELLENCE_IDP_PRIVATE_KEY_PEM" => nil) do
      post "/api/v1/agent/sts/token",
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json
    end

    assert_response :service_unavailable
    assert_equal "server_error", json_body["error"]
  end

  private

  def setup_assigned_node
    _user, organization = create_owner_and_org
    organization.update!(
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: POOL_A,
      workload_identity_provider: PROVIDER_A
    )
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      service_account_email: "env-runtime@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider
    )
    node, access_token, _refresh_token = issue_test_node!(organization: organization, name: "node-1")
    node.update!(environment: environment)
    [access_token, organization, environment, node]
  end

  def setup_unassigned_node
    _user, organization = create_owner_and_org
    organization.update!(
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: POOL_A,
      workload_identity_provider: PROVIDER_A
    )
    node, access_token, _refresh_token = issue_test_node!(organization: organization, name: "node-1")
    [access_token, organization, nil, node]
  end

  def create_owner_and_org
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    [user, organization]
  end

  def with_rsa_signing_key
    rsa = OpenSSL::PKey::RSA.generate(2048)
    with_env("DEVOPSELLENCE_IDP_PRIVATE_KEY_PEM" => rsa.to_pem) { yield }
  end

  def decode_jwt_payload(token)
    _header, payload, _signature = token.split(".", 3)
    JSON.parse(Base64.urlsafe_decode64(pad_base64(payload)))
  end

  def pad_base64(value)
    value + ("=" * ((4 - (value.length % 4)) % 4))
  end

  def json_body
    JSON.parse(response.body)
  end
end
