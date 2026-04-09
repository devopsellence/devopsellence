# frozen_string_literal: true

require "securerandom"
require "test_helper"

class ApiAgentIngressCertificatesTest < ActionDispatch::IntegrationTest
  include ActiveSupport::Testing::TimeHelpers

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
      cloudflare_tunnel_id: "tunnel-1",
      gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
      status: EnvironmentIngress::STATUS_PENDING,
      provisioned_at: Time.current
    )
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
