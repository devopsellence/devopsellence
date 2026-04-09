# frozen_string_literal: true

require "test_helper"

module NodeBundles
  class ProvisionerTest < ActiveSupport::TestCase
    test "standalone runtime provisions a warm bundle without gcp readiness checks" do
      with_env(
        "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
        "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
      ) do
        organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
        project = organization.projects.create!(name: "ShopApp")
        environment = project.environments.create!(name: "production")
        runtime = RuntimeProject.default!
        organization_bundle = OrganizationBundle.create!(
          runtime_project: runtime,
          claimed_by_organization: organization,
          status: OrganizationBundle::STATUS_CLAIMED
        )
        environment_bundle = EnvironmentBundle.create!(
          runtime_project: runtime,
          organization_bundle: organization_bundle,
          claimed_by_environment: environment,
          status: EnvironmentBundle::STATUS_CLAIMED
        )

        broker = mock("broker")
        broker.expects(:ensure_node_bundle_impersonation!).once.returns(
          Runtime::Broker::StandaloneClient::Result.new(status: :ready, message: nil)
        )
        Gcp::NodeBundleReadiness.expects(:new).never

        bundle = NodeBundles::Provisioner.new(environment_bundle:, broker: broker).call

        assert_equal NodeBundle::STATUS_WARM, bundle.status
        assert_predicate bundle.provisioned_at, :present?
      end
    end
  end
end
