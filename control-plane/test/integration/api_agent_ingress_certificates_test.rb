# frozen_string_literal: true

require "securerandom"
require "test_helper"

class ApiAgentIngressCertificatesTest < ActionDispatch::IntegrationTest
  include ActiveSupport::Testing::TimeHelpers

  test "accepts a secondary configured ingress hostname" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES,
      ingress_strategy: Environment::INGRESS_STRATEGY_DIRECT_DNS,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    primary_hostname = random_ingress_hostname
    secondary_hostname = random_ingress_hostname
    ingress = environment.create_environment_ingress!(
      hostname: primary_hostname,
      status: EnvironmentIngress::STATUS_PENDING,
      provisioned_at: Time.current
    )
    ingress.assign_hosts!([ primary_hostname, secondary_hostname ])
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
    node, access_token, = issue_test_node!(organization: organization, name: "node-a")
    node.update!(
      environment: environment,
      public_ip: "203.0.113.10",
      capabilities_json: JSON.generate([Node::CAPABILITY_DIRECT_DNS_INGRESS])
    )

    issuer_result = Struct.new(:certificate_pem, :not_after).new("cert-pem", 30.days.from_now)
    IngressCertificates::Issuer.any_instance.stubs(:call).returns(issuer_result)

    post "/api/v1/agent/ingress_certificates",
      params: { hostname: secondary_hostname, csr: "csr" },
      headers: {
        "Authorization" => "Bearer #{access_token}",
        "devopsellence-agent-capabilities" => Node::CAPABILITY_DIRECT_DNS_INGRESS
      },
      as: :json

    assert_response :created
    payload = JSON.parse(response.body)
    assert_equal secondary_hostname, payload.fetch("hostname")
  end

  test "returns retry-after when certificate issuance is rate limited" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES,
      ingress_strategy: Environment::INGRESS_STRATEGY_DIRECT_DNS,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    hostname = random_ingress_hostname
    environment.create_environment_ingress!(
      hostname: hostname,
      status: EnvironmentIngress::STATUS_PENDING,
      provisioned_at: Time.current
    )
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
    node, access_token, = issue_test_node!(organization: organization, name: "node-a")
    node.update!(
      environment: environment,
      public_ip: "203.0.113.10",
      capabilities_json: JSON.generate([Node::CAPABILITY_DIRECT_DNS_INGRESS])
    )

    error = %(too many failed authorizations (5) for "#{hostname}" in the last 1h0m0s, retry after 2026-04-01 18:58:13 UTC: see https://letsencrypt.org/docs/rate-limits/#authorization-failures-per-identifier-per-account)
    IngressCertificates::Issuer.any_instance.stubs(:call).raises(StandardError.new(error))

    travel_to Time.zone.parse("2026-04-01 18:57:00 UTC") do
      post "/api/v1/agent/ingress_certificates",
        params: { hostname:, csr: "csr" },
        headers: {
          "Authorization" => "Bearer #{access_token}",
          "devopsellence-agent-capabilities" => Node::CAPABILITY_DIRECT_DNS_INGRESS
        },
        as: :json
    end

    assert_response :too_many_requests
    payload = JSON.parse(response.body)
    assert_equal "rate_limited", payload.fetch("error")
    assert_equal error, payload.fetch("error_description")
    assert_equal "73", response.headers.fetch("Retry-After")
    assert_equal Node::INGRESS_TLS_FAILED, node.reload.ingress_tls_status
    assert_equal error, node.ingress_tls_last_error
  end
end
