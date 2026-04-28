# frozen_string_literal: true

require "test_helper"

class NodeTest < ActiveSupport::TestCase
  test "desired_state_uri uses node-attached runtime instead of global backend" do
    with_env("DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone") do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      runtime = RuntimeProject.create!(
        name: "GCP Runtime",
        slug: "gcp-runtime-#{SecureRandom.hex(3)}",
        kind: RuntimeProject::KIND_DEDICATED,
        runtime_backend: RuntimeProject::BACKEND_GCP,
        gcp_project_id: "project-#{SecureRandom.hex(3)}",
        gcp_project_number: "123456789012",
        workload_identity_pool: "projects/123456789012/locations/global/workloadIdentityPools/pool-#{SecureRandom.hex(2)}",
        workload_identity_provider: "projects/123456789012/locations/global/workloadIdentityPools/pool-#{SecureRandom.hex(2)}/providers/provider-#{SecureRandom.hex(2)}",
        gar_region: "us-central1",
        gcs_bucket_prefix: "bucket-prefix-#{SecureRandom.hex(2)}"
      )
      organization_bundle = OrganizationBundle.create!(
        runtime_project: runtime,
        gcs_bucket_name: "bucket-a",
        gar_repository_name: "repo-a",
        gar_repository_region: "us-central1",
        gar_writer_service_account_email: "writer@example.test",
        status: OrganizationBundle::STATUS_CLAIMED,
        claimed_by_organization: organization
      )
      environment = organization.projects.create!(name: "project-a").environments.create!(
        name: "production",
        runtime_project: runtime,
        environment_bundle: EnvironmentBundle.create!(
          runtime_project: runtime,
          organization_bundle: organization_bundle,
          claimed_by_environment: nil,
          service_account_email: "runtime@example.test",
          hostname: "env.example.test",
          status: EnvironmentBundle::STATUS_CLAIMED,
          provisioned_at: Time.current
        )
      )
      node_bundle = NodeBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        environment_bundle: environment.environment_bundle,
        status: NodeBundle::STATUS_CLAIMED,
        desired_state_object_path: "nodes/node-a/desired_state.json"
      )
      node, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(
        environment: environment,
        node_bundle: node_bundle,
        desired_state_bucket: "bucket-a",
        desired_state_object_path: "nodes/node-a/desired_state.json"
      )

      assert_equal "gs://bucket-a/nodes/node-a/desired_state.json", node.desired_state_uri
    end
  end

  test "touch_last_seen_at_if_stale updates blank timestamp" do
    node, _access, _refresh = issue_test_node!(organization: nil)

    freeze_time do
      assert_equal true, node.touch_last_seen_at_if_stale!
      assert_equal Time.current.to_i, node.reload.last_seen_at.to_i
    end
  end

  test "touch_last_seen_at_if_stale skips recent timestamp" do
    node, _access, _refresh = issue_test_node!(organization: nil)
    node.update!(last_seen_at: 30.seconds.ago)

    freeze_time do
      assert_equal false, node.touch_last_seen_at_if_stale!
      assert_in_delta 30.seconds.ago.to_f, node.reload.last_seen_at.to_f, 1
    end
  end

  test "touch_last_seen_at_if_stale refreshes old timestamp" do
    node, _access, _refresh = issue_test_node!(organization: nil)
    node.update!(last_seen_at: 2.minutes.ago)

    freeze_time do
      assert_equal true, node.touch_last_seen_at_if_stale!
      assert_equal Time.current.to_i, node.reload.last_seen_at.to_i
    end
  end

  test "touch_last_seen_at_if_stale skips when row lock times out" do
    node, _access, _refresh = issue_test_node!(organization: nil)
    node.update!(last_seen_at: 2.minutes.ago)
    node.stubs(:with_last_seen_lock_timeout).raises(ActiveRecord::LockWaitTimeout, "canceling statement due to lock timeout")

    freeze_time do
      assert_equal false, node.touch_last_seen_at_if_stale!
      assert_in_delta 2.minutes.ago.to_f, node.reload.last_seen_at.to_f, 1
    end
  end
end
