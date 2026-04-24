# frozen_string_literal: true

require "securerandom"
require "test_helper"

module EnvironmentBundles
  class ProvisionerTest < ActiveSupport::TestCase
    ReadyResult = Struct.new(:status, :message, keyword_init: true)

    class FakeBroker
      def provision_environment_bundle!(bundle:)
        ReadyResult.new(status: :ready, message: nil)
      end

      def upsert_environment_bundle_tunnel_secret!(bundle:, tunnel_token:)
        ReadyResult.new(status: :ready, message: nil)
      end
    end

    test "bundle hostname allocation skips secondary ingress hosts" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      runtime = RuntimeProject.default!
      organization_bundle = ensure_test_organization_bundle!(organization, runtime:, status: OrganizationBundle::STATUS_CLAIMED)
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
        hostname: "primary.local.devopsellence.test",
        cloudflare_tunnel_id: "tunnel-1",
        gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
        status: EnvironmentIngress::STATUS_READY,
        provisioned_at: Time.current
      )
      ingress.assign_hosts!(["primary.local.devopsellence.test", "taken.local.devopsellence.test"])

      SecureRandom.stubs(:alphanumeric).returns("taken", "fresh")

      with_runtime_config(
        ingress_backend: "local",
        local_ingress_public_url: "http://127.0.0.1:18080",
        local_ingress_hostname_suffix: "local.devopsellence.test"
      ) do
        bundle = Provisioner.new(
          organization_bundle: organization_bundle,
          broker: FakeBroker.new
        ).call

        assert_equal "fresh.local.devopsellence.test", bundle.hostname
      end
    end
  end
end
