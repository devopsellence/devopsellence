# frozen_string_literal: true

require "test_helper"

class NodesCleanupTest < ActiveSupport::TestCase
  include ActiveJob::TestHelper

  test "unassigns node and revokes node access" do
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
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rel-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      status: Release::STATUS_PUBLISHED,
      published_at: Time.current
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
      desired_state_sequence: 4
    )
    node_bundle.update!(node: node)
    revoked_at = Time.utc(2026, 3, 14, 12, 0, 0)

    result = Nodes::Cleanup.new(node: node, revoked_at: revoked_at, broker: fake_broker).call

    assert_equal environment.id, result.environment.id
    assert_nil node.reload.environment_id
    assert_nil node.node_bundle_id
    assert_equal "", node.desired_state_bucket
    assert_equal "", node.desired_state_object_path
    assert_equal revoked_at, node.revoked_at
    assert_equal revoked_at, node.access_expires_at
    assert_equal revoked_at, node.refresh_expires_at
    assert_not NodeBundle.exists?(node_bundle.id)
    assert_nil result.desired_state
  end

  test "revokes node even when runtime desired state path is missing" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    node, = issue_test_node!(organization: organization, name: "node-a")
    node.update!(desired_state_bucket: "", desired_state_object_path: "")

    result = Nodes::Cleanup.new(node: node).call

    assert_nil result.desired_state
    assert_not_nil node.reload.revoked_at
  end

  test "cleanup schedules managed server delete for managed nodes" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    environment = organization.projects.create!(name: "Project A").environments.create!(
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
      status: NodeBundle::STATUS_CLAIMED
    )
    node, = issue_test_node!(
      organization: organization,
      name: "node-a",
      managed: true,
      managed_provider: "hetzner",
      managed_region: "ash",
      managed_size_slug: "cpx11",
      provider_server_id: "server-123"
    )
    node.update!(
      environment: environment,
      node_bundle: node_bundle,
      desired_state_bucket: node_bundle.desired_state_bucket,
      desired_state_object_path: node_bundle.desired_state_object_path
    )
    node_bundle.update!(node: node)

    assert_enqueued_with(job: ManagedNodes::DeleteJob, args: [ { node_id: node.id } ]) do
      Nodes::Cleanup.new(node: node, broker: fake_broker).call
    end

    assert_nil node.reload.node_bundle_id
    assert_equal "", node.desired_state_bucket
    assert_equal "", node.desired_state_object_path
    assert_not NodeBundle.exists?(node_bundle.id)
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
