# frozen_string_literal: true

require "test_helper"

class NodesRetireTest < ActiveSupport::TestCase
  test "retires customer-managed node and destroys claimed node bundle" do
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
    environment_bundle = ensure_test_environment_bundle!(environment)
    node_bundle = NodeBundle.create!(
      runtime_project: environment_bundle.runtime_project,
      organization_bundle: environment_bundle.organization_bundle,
      environment_bundle: environment_bundle,
      node: nil,
      status: NodeBundle::STATUS_CLAIMED
    )
    node, = issue_test_node!(organization: organization, name: "node-a")
    node.update!(
      node_bundle: node_bundle,
      desired_state_bucket: node_bundle.desired_state_bucket,
      desired_state_object_path: node_bundle.desired_state_object_path
    )
    node_bundle.update!(node: node)
    revoked_at = Time.utc(2026, 4, 9, 12, 0, 0)

    result = Nodes::Retire.new(node: node, revoked_at: revoked_at, broker: fake_broker).call

    assert_equal revoked_at, result.revoked_at
    assert_not Node.exists?(node.id)
    assert_not NodeBundle.exists?(node_bundle.id)
  end

  test "retires customer-managed node without resolving broker when no bundle is claimed" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    node, = issue_test_node!(organization: organization, name: "node-a")
    Runtime::Broker.expects(:current).never

    Nodes::Retire.new(node: node).call

    assert_not Node.exists?(node.id)
  end

  private

  def fake_broker
    mock("broker").tap do |broker|
      broker.stubs(:revoke_node_bundle_impersonation!).returns(
        Runtime::Broker::LocalClient::Result.new(status: :ready, message: nil)
      )
    end
  end
end
