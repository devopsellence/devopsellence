# frozen_string_literal: true

require "test_helper"

module Nodes
  class BootstrapAssignmentJobTest < ActiveJob::TestCase
    test "repairs partial same-environment assignments" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: organization.gcp_project_id,
        gcp_project_number: organization.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider,
        service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
        runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
      )
      environment_bundle = ensure_test_environment_bundle!(environment)
      node, = issue_test_node!(organization: organization, name: "node-a")
      bundle = environment_bundle.node_bundles.create!(
        runtime_project: environment.runtime_project,
        organization_bundle: organization.organization_bundle,
        status: NodeBundle::STATUS_CLAIMED,
        claimed_at: Time.current,
        provisioned_at: 1.hour.ago,
        node: node
      )
      node.update!(
        environment: environment,
        node_bundle: bundle,
        desired_state_bucket: bundle.desired_state_bucket,
        desired_state_object_path: bundle.desired_state_object_path,
        desired_state_sequence: 0
      )

      store = FakeObjectStore.new

      with_object_store(store) do
        BootstrapAssignmentJob.perform_now(node_id: node.id, environment_id: environment.id, issuer: "https://dev.devopsellence.com")
      end

      node.reload
      assert node.assignment_ready?
      assert store.writes.any?
    end
  end
end
