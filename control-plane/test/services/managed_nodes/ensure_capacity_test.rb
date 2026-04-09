# frozen_string_literal: true

require "test_helper"

class ManagedNodesEnsureCapacityTest < ActiveSupport::TestCase
  class FakeDeleteServer
    cattr_accessor :deleted_node_ids, default: []

    def initialize(node:, **)
      @node = node
    end

    def call
      self.class.deleted_node_ids << @node.id
      @node.update!(provisioning_status: Node::PROVISIONING_DELETING)
    end
  end

  setup do
    FakeDeleteServer.deleted_node_ids = []
  end

  test "reuses existing assigned managed node when heartbeat is fresh" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project     = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name:                       "production",
      gcp_project_id:             organization.gcp_project_id,
      gcp_project_number:         organization.gcp_project_number,
      workload_identity_pool:     organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email:      "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
      runtime_kind:               Environment::RUNTIME_MANAGED
    )
    claim_test_environment_bundle!(organization:, environment:)
    release = project.releases.create!(
      git_sha: "a" * 40, revision: "r1", image_repository: "app",
      image_digest: "sha256:#{"b" * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )
    node, = issue_test_node!(organization: organization, name: "assigned-node",
      managed: true, managed_provider: "hetzner", managed_region: "ash",
      managed_size_slug: "cpx11", provider_server_id: "server-1")
    node.update!(environment: environment, last_seen_at: Time.current,
                 provisioning_status: Node::PROVISIONING_READY,
                 lease_expires_at: 30.minutes.from_now)

    result = ManagedNodes::EnsureCapacity.new(
      environment: environment, release: release,
      issuer: "https://dev.devopsellence.com"
    ).call

    assert_equal [ node.id ], result.nodes.map(&:id)
    assert_equal false, result.claimed_from_pool
    assert_equal environment.id, node.reload.environment_id
  end

  test "claims warm server and assigns via bundle for first deploy" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project     = organization.projects.create!(name: "BundleApp")
    environment = project.environments.create!(
      name:                       "production",
      gcp_project_id:             organization.gcp_project_id,
      gcp_project_number:         organization.gcp_project_number,
      workload_identity_pool:     organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind:               Environment::RUNTIME_MANAGED,
      runtime_project:            RuntimeProject.default!
    )
    _organization_bundle, environment_bundle = claim_test_environment_bundle!(organization:, environment:)
    release = project.releases.create!(
      git_sha: "a" * 40, revision: "r1", image_repository: "app",
      image_digest: "sha256:#{"b" * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )

    # Create a warm server in the pool (no bundle, no org, no env)
    warm_node, = issue_test_node!(organization: nil, name: "warm-server",
      managed: true, managed_provider: "hetzner", managed_region: "ash",
      managed_size_slug: "cpx11", provider_server_id: "srv-warm-1")
    warm_node.update!(organization: nil, desired_state_bucket: "", desired_state_object_path: "")

    store = FakeObjectStore.new
    result = nil
    with_object_store(store) do
      with_fake_broker do
        result = ManagedNodes::EnsureCapacity.new(
          environment: environment, release: release,
          issuer: "https://dev.devopsellence.com"
        ).call

        assert_equal 1, result.nodes.size
        assert_equal true, result.claimed_from_pool
      end
    end

    claimed_node = result.nodes.first.reload
    assert_equal environment.id, claimed_node.environment_id
    assert_equal organization.id, claimed_node.organization_id
    assert claimed_node.node_bundle.present?
    assert claimed_node.desired_state_bucket.present?
  end

  test "retires stale assigned node and claims new warm server" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project     = organization.projects.create!(name: "ShopApp")
    environment = project.environments.create!(
      name:                       "production",
      gcp_project_id:             organization.gcp_project_id,
      gcp_project_number:         organization.gcp_project_number,
      workload_identity_pool:     organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind:               Environment::RUNTIME_MANAGED,
      runtime_project:            RuntimeProject.default!
    )
    _organization_bundle, environment_bundle = claim_test_environment_bundle!(organization:, environment:)
    release = project.releases.create!(
      git_sha: "a" * 40, revision: "r1", image_repository: "app",
      image_digest: "sha256:#{"b" * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )
    stale_node, = issue_test_node!(organization: organization, name: "stale-node",
      managed: true, managed_provider: "hetzner", managed_region: "ash",
      managed_size_slug: "cpx11", provider_server_id: "server-gone")
    stale_node.update!(environment: environment, last_seen_at: 30.minutes.ago,
                       provisioning_status: Node::PROVISIONING_READY)

    # Create a warm server (decoupled from bundles)
    warm_node, = issue_test_node!(organization: nil, name: "warm-server",
      managed: true, managed_provider: "hetzner", managed_region: "ash",
      managed_size_slug: "cpx11", provider_server_id: "server-new")
    warm_node.update!(organization: nil, desired_state_bucket: "", desired_state_object_path: "")

    store = FakeObjectStore.new
    result = nil
    with_object_store(store) do
      with_fake_broker do
        result = ManagedNodes::EnsureCapacity.new(
          environment:       environment,
          release:           release,
          issuer:            "https://dev.devopsellence.com",
          retire_node_class: FakeDeleteServer
        ).call

        assert_equal 1, result.nodes.size
        assert_equal true, result.claimed_from_pool
        assert_includes FakeDeleteServer.deleted_node_ids, stale_node.id
      end
    end
  end

  private

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
      gcp_secret_name: "envbundle-#{SecureRandom.hex(3)}-secret",
      hostname: random_ingress_hostname,
      cloudflare_tunnel_id: "tunnel-#{SecureRandom.hex(2)}",
      status: EnvironmentBundle::STATUS_CLAIMED
    )
    environment.update!(environment_bundle: environment_bundle, service_account_email: environment_bundle.service_account_email)

    # Pre-provision warm node bundles
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
end
