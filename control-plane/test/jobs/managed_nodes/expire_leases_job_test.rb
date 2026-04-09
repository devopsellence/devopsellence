# frozen_string_literal: true

require "test_helper"

module ManagedNodes
  class ExpireLeasesJobTest < ActiveJob::TestCase
    class FakeDeleteServer
      cattr_accessor :deleted_provider_ids, default: []

      def initialize(node:)
        @node = node
      end

      def call
        self.class.deleted_provider_ids << @node.provider_server_id
      end
    end

    setup do
      FakeDeleteServer.deleted_provider_ids = []
    end

    test "retires expired managed nodes" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "ShopApp")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: organization.gcp_project_id,
        gcp_project_number: organization.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider,
        service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
      )
      node, = issue_test_node!(
        organization: organization,
        managed: true,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11",
        provider_server_id: "server-1"
      )
      node.update!(environment:, lease_expires_at: 5.minutes.ago)

      ManagedNodes::DeleteServer.stubs(:new).returns(FakeDeleteServer.new(node: node))
      ManagedNodes::ExpireLeasesJob.perform_now

      assert_equal [ "server-1" ], FakeDeleteServer.deleted_provider_ids
      assert_nil Node.find_by(id: node.id)
    end

    test "returns stuck managed nodes to the warm pool and releases claimed bundles" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "ShopApp")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: organization.gcp_project_id,
        gcp_project_number: organization.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider,
        service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
      )
      environment_bundle = ensure_test_environment_bundle!(environment)
      node, = issue_test_node!(
        organization: nil,
        managed: true,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11",
        provider_server_id: "server-2"
      )
      node.update!(
        lease_expires_at: 5.minutes.ago,
        desired_state_bucket: "stale-bucket",
        desired_state_object_path: "nodes/stale/desired_state.json"
      )
      bundle = NodeBundle.create!(
        runtime_project: environment.runtime_project,
        organization_bundle: organization.organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_CLAIMED,
        claimed_at: 1.minute.ago,
        node: node,
        provisioned_at: 1.hour.ago
      )

      ManagedNodes::ExpireLeasesJob.perform_now

      assert_nil node.reload.lease_expires_at
      assert_nil node.node_bundle_id
      assert_equal "", node.desired_state_bucket
      assert_equal "", node.desired_state_object_path
      assert_equal NodeBundle::STATUS_WARM, bundle.reload.status
      assert_nil bundle.claimed_at
      assert_nil bundle.node_id
    end
  end
end
