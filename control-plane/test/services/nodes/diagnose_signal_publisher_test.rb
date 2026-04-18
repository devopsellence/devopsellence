# frozen_string_literal: true

require "test_helper"

class NodesDiagnoseSignalPublisherTest < ActiveSupport::TestCase
  test "republishes assigned desired state to signal diagnose intent" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_MANAGED
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rev-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{"b" * 64}",
      runtime_json: release_runtime_json
    )
    environment.update!(current_release: release)
    environment_bundle = ensure_test_environment_bundle!(environment)
    node_bundle = NodeBundle.create!(
      runtime_project: environment_bundle.runtime_project,
      organization_bundle: environment_bundle.organization_bundle,
      environment_bundle: environment_bundle,
      status: NodeBundle::STATUS_CLAIMED
    )
    node, = issue_test_node!(organization: organization, name: "node-a")
    node.update!(
      environment: environment,
      node_bundle: node_bundle,
      desired_state_bucket: node_bundle.desired_state_bucket,
      desired_state_object_path: node_bundle.desired_state_object_path,
      desired_state_sequence: 1
    )

    store = FakeObjectStore.new

    published = nil
    with_object_store(store) do
      published = Nodes::DiagnoseSignalPublisher.new(node: node).call
    end

    assert_equal true, published
    assert_equal 2, node.reload.desired_state_sequence
    desired_state = store.desired_state_payload(bucket: node.desired_state_bucket, object_path: node.desired_state_object_path)
    assert_equal "rev-1", desired_state.fetch("revision")
    assert_equal 2, desired_state.fetch("assignmentSequence")
  end

  test "skips nodes without an assigned desired state target" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    node, = issue_test_node!(organization: organization, name: "node-a")

    Nodes::DesiredStatePublisher.expects(:new).never

    assert_equal false, Nodes::DiagnoseSignalPublisher.new(node: node).call
  end
end
