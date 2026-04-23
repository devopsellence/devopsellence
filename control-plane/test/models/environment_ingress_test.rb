# frozen_string_literal: true

require "securerandom"
require "test_helper"

class EnvironmentIngressTest < ActiveSupport::TestCase
  test "normalizes and de-duplicates hosts case-insensitively" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
    )

    ingress = environment.create_environment_ingress!(
      hostname: "APP.Example.Test",
      cloudflare_tunnel_id: "tunnel-1",
      gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )

    ingress.assign_hosts!([ "WWW.Example.Test", "www.example.test", "Api.Example.Test" ])

    assert_equal [ "www.example.test", "api.example.test" ], ingress.reload.hosts
    assert_equal "www.example.test", ingress.hostname
    assert ingress.hostname_matches?("API.EXAMPLE.TEST")
  end
end
