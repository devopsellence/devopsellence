# frozen_string_literal: true

require "test_helper"

module NodeBundles
  class ClaimTest < ActiveSupport::TestCase
    test "reuses an in-flight provisioning bundle before creating another" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "ShopApp")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: organization.gcp_project_id,
        gcp_project_number: organization.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider,
        service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
        runtime_kind: Environment::RUNTIME_MANAGED
      )
      environment_bundle = ensure_test_environment_bundle!(environment)
      provisioning_bundle = NodeBundle.create!(
        runtime_project: environment.runtime_project,
        organization_bundle: organization.organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_PROVISIONING
      )
      node, = issue_test_node!(
        organization: nil,
        name: "warm-node",
        managed: true,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11",
        provider_server_id: "srv-1"
      )
      node.update_columns(labels_json: "[]", lease_expires_at: 10.minutes.from_now)

      provisioner_class = stub("provisioner_class")
      provisioner_class.expects(:new).never
      sleeper = ->(_duration) do
        provisioning_bundle.update!(
          status: NodeBundle::STATUS_WARM,
          provisioned_at: Time.current
        )
      end

      NodeBundles::Claim.new(
        environment: environment,
        node: node,
        provisioner_class: provisioner_class,
        publish_assignment_state: false,
        existing_provisioning_wait_timeout: 5.seconds,
        existing_provisioning_poll_interval: 1.second,
        sleeper: sleeper
      ).call

      assert_equal environment.id, node.reload.environment_id
      assert_equal provisioning_bundle.id, node.node_bundle_id
      assert_equal NodeBundle::STATUS_CLAIMED, provisioning_bundle.reload.status
    end

    test "prefers a newly warm bundle even while spare bundles are still provisioning" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "ShopApp")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: organization.gcp_project_id,
        gcp_project_number: organization.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider,
        service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
        runtime_kind: Environment::RUNTIME_MANAGED
      )
      environment_bundle = ensure_test_environment_bundle!(environment)
      claim_target = NodeBundle.create!(
        runtime_project: environment.runtime_project,
        organization_bundle: organization.organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_PROVISIONING
      )
      NodeBundle.create!(
        runtime_project: environment.runtime_project,
        organization_bundle: organization.organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_PROVISIONING
      )
      node, = issue_test_node!(
        organization: nil,
        name: "warm-node",
        managed: true,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11",
        provider_server_id: "srv-1"
      )
      node.update_columns(labels_json: "[]", lease_expires_at: 10.minutes.from_now)

      provisioner_class = stub("provisioner_class")
      provisioner_class.expects(:new).never
      sleeper = ->(_duration) do
        claim_target.update!(
          status: NodeBundle::STATUS_WARM,
          provisioned_at: Time.current
        )
      end

      NodeBundles::Claim.new(
        environment: environment,
        node: node,
        provisioner_class: provisioner_class,
        publish_assignment_state: false,
        existing_provisioning_wait_timeout: 5.seconds,
        existing_provisioning_poll_interval: 1.second,
        sleeper: sleeper
      ).call

      assert_equal claim_target.id, node.reload.node_bundle_id
      assert_equal NodeBundle::STATUS_CLAIMED, claim_target.reload.status
    end

    test "releases the bundle and node lease when association fails before assignment completes" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "ShopApp")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: organization.gcp_project_id,
        gcp_project_number: organization.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider,
        service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
        runtime_kind: Environment::RUNTIME_MANAGED
      )
      environment_bundle = ensure_test_environment_bundle!(environment)
      bundle = NodeBundle.create!(
        runtime_project: environment.runtime_project,
        organization_bundle: organization.organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_WARM,
        provisioned_at: 1.hour.ago
      )
      node, = issue_test_node!(
        organization: nil,
        name: "warm-node",
        managed: true,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11",
        provider_server_id: "srv-1"
      )
      node.update_columns(labels_json: "{}", lease_expires_at: 10.minutes.from_now)

      assert_raises(NodeBundles::Claim::Error) do
        NodeBundles::Claim.new(environment: environment, node: node, publish_assignment_state: false).call
      end

      assert_nil node.reload.organization_id
      assert_nil node.environment_id
      assert_nil node.node_bundle_id
      assert_nil node.lease_expires_at
      assert_equal "", node.desired_state_bucket
      assert_equal "", node.desired_state_object_path
      assert_equal NodeBundle::STATUS_WARM, bundle.reload.status
      assert_nil bundle.claimed_at
      assert_nil bundle.node_id
    end

    test "releases the bundle for customer-managed nodes when association fails before assignment completes" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "ShopApp")
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
      bundle = NodeBundle.create!(
        runtime_project: environment.runtime_project,
        organization_bundle: organization.organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_WARM,
        provisioned_at: 1.hour.ago
      )
      node, = issue_test_node!(organization: organization, name: "customer-node")
      node.update_columns(labels_json: "{}", lease_expires_at: 10.minutes.from_now)

      assert_raises(NodeBundles::Claim::Error) do
        NodeBundles::Claim.new(environment: environment, node: node, publish_assignment_state: false).call
      end

      assert_equal organization.id, node.reload.organization_id
      assert_nil node.environment_id
      assert_nil node.node_bundle_id
      assert_nil node.lease_expires_at
      assert_equal "", node.desired_state_bucket
      assert_equal "", node.desired_state_object_path
      assert_equal NodeBundle::STATUS_WARM, bundle.reload.status
      assert_nil bundle.claimed_at
      assert_nil bundle.node_id
    end
  end
end
