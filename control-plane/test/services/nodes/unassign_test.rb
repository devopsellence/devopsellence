# frozen_string_literal: true

require "test_helper"

class NodesUnassignTest < ActiveSupport::TestCase
  test "unassigns customer node and destroys claimed node bundle" do
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
      environment: environment,
      node_bundle: node_bundle,
      desired_state_bucket: node_bundle.desired_state_bucket,
      desired_state_object_path: node_bundle.desired_state_object_path,
      desired_state_sequence: 4
    )
    node_bundle.update!(node: node)

    fake_broker = mock("broker")
    fake_broker.stubs(:revoke_node_bundle_impersonation!).returns(
      Runtime::Broker::LocalClient::Result.new(status: :ready, message: nil)
    )

    Nodes::Unassign.new(node: node, broker: fake_broker).call

    assert_nil node.reload.environment_id
    assert_nil node.node_bundle_id
    assert_equal "", node.desired_state_bucket
    assert_equal "", node.desired_state_object_path
    assert Node.exists?(node.id)
    assert_not NodeBundle.exists?(node_bundle.id)
    assert node.access_active?
    assert node.refresh_active?
  end
end
