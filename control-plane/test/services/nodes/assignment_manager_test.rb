# frozen_string_literal: true

require "test_helper"

class NodesAssignmentManagerTest < ActiveSupport::TestCase
  include ActiveJob::TestHelper

  test "assigns node to environment through bundle claim" do
    organization, environment, node = setup_assignment_scenario

    store = FakeObjectStore.new
    with_object_store(store) do
      with_fake_broker do
        Nodes::AssignmentManager.new(
          node: node,
          environment: environment,
          issuer: "https://dev.devopsellence.com"
        ).call
      end
    end

    node.reload
    assert_equal environment.id, node.environment_id
    assert_equal organization.id, node.organization_id
    assert node.node_bundle.present?
    assert node.desired_state_bucket.present?
    assert node.desired_state_object_path.present?
  end

  test "sets lease for managed nodes" do
    organization, environment, node = setup_assignment_scenario(managed: true)

    store = FakeObjectStore.new
    with_object_store(store) do
      with_fake_broker do
        Nodes::AssignmentManager.new(
          node: node,
          environment: environment,
          issuer: "https://dev.devopsellence.com"
        ).call
      end
    end

    node.reload
    assert node.lease_expires_at.present?
    assert node.lease_expires_at > Time.current
  end

  test "does not set lease for customer nodes" do
    organization, environment, node = setup_assignment_scenario(managed: false)

    store = FakeObjectStore.new
    with_object_store(store) do
      with_fake_broker do
        Nodes::AssignmentManager.new(
          node: node,
          environment: environment,
          issuer: "https://dev.devopsellence.com"
        ).call
      end
    end

    node.reload
    assert_nil node.lease_expires_at
  end

  test "treats same-environment assignment as idempotent" do
    organization, environment, node = setup_assignment_scenario
    existing_bundle = node_environment_bundle(environment).node_bundles.create!(
      runtime_project: environment.runtime_project || RuntimeProject.default!,
      organization_bundle: organization.organization_bundle,
      status: NodeBundle::STATUS_CLAIMED,
      claimed_at: Time.current,
      provisioned_at: 1.hour.ago,
      node: node
    )
    node.update!(
      environment: environment,
      node_bundle: existing_bundle,
      desired_state_bucket: existing_bundle.desired_state_bucket,
      desired_state_object_path: existing_bundle.desired_state_object_path,
      desired_state_sequence: 2
    )

    NodeBundles::Claim.any_instance.expects(:call).never

    result = Nodes::AssignmentManager.new(
      node: node,
      environment: environment,
      issuer: "https://dev.devopsellence.com"
    ).call

    assert_equal environment.id, result.previous_environment.id
    assert_equal existing_bundle.id, node.reload.node_bundle_id
  end

  test "repairs a partial same-environment assignment before retrying" do
    organization, environment, node = setup_assignment_scenario
    existing_bundle = node_environment_bundle(environment).node_bundles.create!(
      runtime_project: environment.runtime_project || RuntimeProject.default!,
      organization_bundle: organization.organization_bundle,
      status: NodeBundle::STATUS_CLAIMED,
      claimed_at: Time.current,
      provisioned_at: 1.hour.ago,
      node: node
    )
    node.update!(
      environment: environment,
      node_bundle: existing_bundle,
      desired_state_bucket: existing_bundle.desired_state_bucket,
      desired_state_object_path: existing_bundle.desired_state_object_path,
      desired_state_sequence: 0
    )

    store = FakeObjectStore.new

    NodeBundles::Claim.any_instance.expects(:call).never

    with_object_store(store) do
      result = Nodes::AssignmentManager.new(
        node: node,
        environment: environment,
        issuer: "https://dev.devopsellence.com"
      ).call

      assert_equal environment.id, result.previous_environment.id
    end

    node.reload
    assert_equal existing_bundle.id, node.node_bundle_id
    assert node.assignment_ready?
  end

  test "rejects assigning a customer-managed node to a different organization" do
    organization, environment, node = setup_assignment_scenario(managed: false)
    other_organization = Organization.create!(name: "other-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(other_organization)
    other_project = other_organization.projects.create!(name: "Other Project")
    other_environment = other_project.environments.create!(
      name: "production",
      gcp_project_id: other_organization.gcp_project_id,
      gcp_project_number: other_organization.gcp_project_number,
      workload_identity_pool: other_organization.workload_identity_pool,
      workload_identity_provider: other_organization.workload_identity_provider,
      service_account_email: "env@#{other_organization.gcp_project_id}.iam.gserviceaccount.com",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    claim_test_environment_bundle!(organization: other_organization, environment: other_environment)

    error = assert_raises(Nodes::AssignmentManager::Error) do
      Nodes::AssignmentManager.new(
        node: node,
        environment: other_environment,
        issuer: "https://dev.devopsellence.com"
      ).call
    end

    assert_equal "node belongs to a different organization", error.message
    assert_equal organization.id, node.reload.organization_id
    assert_nil node.environment_id
  end

  test "rejects assigning an unowned customer-managed node" do
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
    claim_test_environment_bundle!(organization:, environment:)
    node, = issue_test_node!(organization: nil, name: "rogue-node")

    error = assert_raises(Nodes::AssignmentManager::Error) do
      Nodes::AssignmentManager.new(
        node: node,
        environment: environment,
        issuer: "https://dev.devopsellence.com"
      ).call
    end

    assert_equal "customer-managed node must belong to the target organization", error.message
    assert_nil node.reload.organization_id
    assert_nil node.environment_id
  end

  private

  def setup_assignment_scenario(managed: false)
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
    claim_test_environment_bundle!(organization:, environment:)

    node_opts = { organization: organization, name: "node-a" }
    if managed
      node_opts.merge!(
        managed: true,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11",
        provider_server_id: "server-1"
      )
    end
    node, = issue_test_node!(**node_opts)

    [ organization, environment, node ]
  end

  def claim_test_environment_bundle!(organization:, environment:)
    runtime = environment.runtime_project || RuntimeProject.default!
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      claimed_by_organization: organization,
      claimed_at: Time.current,
      gcs_bucket_name: organization.gcs_bucket_name,
      gar_repository_name: organization.gar_repository_name,
      gar_repository_region: organization.gar_repository_region,
      gar_writer_service_account_email: "writer-#{SecureRandom.hex(3)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_CLAIMED
    )
    organization.update!(organization_bundle: organization_bundle)

    environment_bundle = EnvironmentBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      claimed_by_environment: environment,
      claimed_at: Time.current,
      service_account_email: "envbundle-#{SecureRandom.hex(3)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      hostname: random_ingress_hostname,
      status: EnvironmentBundle::STATUS_CLAIMED
    )
    environment.update!(environment_bundle: environment_bundle, service_account_email: environment_bundle.service_account_email)

    # Pre-provision a warm node bundle for the claim to use
    NodeBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      environment_bundle: environment_bundle,
      status: NodeBundle::STATUS_WARM,
      provisioned_at: 1.hour.ago
    )

    [ organization_bundle, environment_bundle ]
  end

  def with_fake_broker
    fake_result = Struct.new(:status, :message, keyword_init: true).new(status: :ready, message: nil)
    fake_broker = mock("broker")
    fake_broker.stubs(:ensure_node_bundle_impersonation!).returns(fake_result)
    Runtime::Broker.stubs(:current).returns(fake_broker)
    yield
  end

  def node_environment_bundle(environment)
    environment.environment_bundle || ensure_test_environment_bundle!(environment)
  end
end
