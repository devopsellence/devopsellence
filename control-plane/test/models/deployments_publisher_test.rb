# frozen_string_literal: true

require "json"
require "securerandom"
require "test_helper"

class DeploymentsPublisherTest < ActiveSupport::TestCase
  include ActiveJob::TestHelper

  test "publishes release task desired state to the executor node before rollout" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json(tasks: {
        "release" => { "service" => "web", "command" => ["bundle", "exec", "rails", "db:migrate"] }
      }),
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(
      environment: environment,
      capabilities: [Node::CAPABILITY_RELEASE_TASK]
    )
    store = FakeObjectStore.new

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)

    result = Deployments::Publisher.new(environment: environment, release: release, store: store).call

    deployment = result.deployment.reload
    assert_equal Deployment::STATUS_ROLLING_OUT, deployment.status
    assert_equal Deployment::RELEASE_TASK_STATUS_PENDING, deployment.release_task_status
    assert_equal node.id, deployment.release_task_node_id
    assert_nil environment.reload.current_release_id
    assert_equal Release::STATUS_DRAFT, release.reload.status

    desired_state = store.desired_state_payload(bucket: organization.gcs_bucket_name, object_path: node.desired_state_object_path)
    task = desired_state_tasks(desired_state).first
    assert_equal "release", task.fetch("name")
    assert_equal release.image_reference_for(organization), task.fetch("image")
    assert_equal 1, deployment_statuses_for(environment).size
    assert_equal "waiting to run release task", deployment_statuses_for(environment).first.message
  end

  test "publishes release and writes node desired state" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json(services: {
        "web" => web_service_runtime(
          env: { "RAILS_ENV" => "production" },
          secret_refs: [ { "name" => "DATABASE_URL", "secret" => "projects/acme/secrets/db/versions/latest" } ]
        )
      }),
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)
    store = FakeObjectStore.new
    hostname = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"
    environment.create_environment_ingress!(
      hostname: hostname,
      cloudflare_tunnel_id: "tunnel-1",
      gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    result = Deployments::Publisher.new(environment: environment, release: release, store: store).call

    assert_equal 1, result.assigned_nodes

    release.reload
    environment.reload

    assert_equal Release::STATUS_PUBLISHED, release.status
    assert_equal release.id, environment.current_release_id
    assert_equal 1, node.reload.desired_state_sequence
    assert_equal 1, deployment_statuses_for(environment).size
    assert_equal DeploymentNodeStatus::PHASE_PENDING, deployment_statuses_for(environment).first.phase

    desired_state = store.desired_state_payload(bucket: organization.gcs_bucket_name, object_path: node.desired_state_object_path)
    service = desired_state_services(desired_state).first
    assert_equal true, desired_state["assigned"]
    assert_equal release.revision, desired_state["revision"]
    assert_equal "Production", desired_state_environment(desired_state).fetch("name")
    assert_equal "web", service.fetch("name")
    assert_equal "web", service.fetch("kind")
    assert_equal release.image_reference_for(organization), service.fetch("image")
    assert_equal 3000, service.dig("ports", 0, "port")
    assert_equal({ "DATABASE_URL" => "projects/acme/secrets/db/versions/latest" }, service.fetch("secretRefs"))
    assert_equal [ hostname ], desired_state.dig("ingress", "hosts")
    assert_equal "gsm://projects/gcp-proj-a/secrets/env-#{environment.id}-ingress-cloudflare-tunnel-token/versions/latest", desired_state.dig("ingress", "tunnelTokenSecretRef")
    assert_equal "Production", desired_state.dig("ingress", "routes", 0, "target", "environment")
    assert_equal "/up", service.dig("healthcheck", "path")
    assert_equal 3000, service.dig("healthcheck", "port")
    assert_equal 5, service.dig("healthcheck", "intervalSeconds")
    assert_equal 3, service.dig("healthcheck", "retries")
  end

  test "publishes immutable desired state object plus pointer and direct desired state path" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json,
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)
    store = FakeObjectStore.new

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    Deployments::Publisher.new(environment: environment, release: release, store: store).call

    direct = store.find_write(bucket: organization.gcs_bucket_name, object_path: node.desired_state_object_path)
    assert_equal "signed_desired_state.v1", direct.dig(:envelope, "format")

    immutable_object_path = Nodes::DesiredStatePointer.sequence_object_path(
      reference_path: node.desired_state_object_path,
      sequence: 1
    )
    immutable = store.find_write(bucket: organization.gcs_bucket_name, object_path: immutable_object_path)
    assert_equal "signed_desired_state.v1", immutable.dig(:envelope, "format")

    pointer_object_path = Nodes::DesiredStatePointer.pointer_object_path(reference_path: node.desired_state_object_path)
    pointer = store.find_write(bucket: organization.gcs_bucket_name, object_path: pointer_object_path)
    assert_equal Nodes::DesiredStatePointer::FORMAT, pointer.dig(:payload, "format")
    assert_equal 1, pointer.dig(:payload, "sequence")
    assert_equal immutable_object_path, pointer.dig(:payload, "object_path")
    assert_equal release.revision, direct.dig(:payload, "revision")
  end

  test "does not rewrite long-lived trial lease on repeat deploy" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}", plan_tier: Organization::PLAN_TIER_TRIAL)
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_MANAGED
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json,
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    lease_expires_at = 55.minutes.from_now
    node.update!(environment: environment, lease_expires_at: lease_expires_at)
    store = FakeObjectStore.new

    ManagedNodes::EnsureCapacity.any_instance.stubs(:call).returns(true)
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)

    Deployments::Publisher.new(environment: environment, release: release, store: store).call

    assert_in_delta lease_expires_at.to_f, node.reload.lease_expires_at.to_f, 1
  end

  test "preserves fully qualified explicit image refs in desired state" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:#{'a' * 64}",
      image_repository: "docker.io/mccutchen/go-httpbin",
      runtime_json: release_runtime_json(services: {
        "web" => web_service_runtime(port: 8080, healthcheck_path: "/status/200")
      }),
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)
    store = FakeObjectStore.new

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    Deployments::Publisher.new(environment: environment, release: release, store: store).call

    desired_state = store.desired_state_payload(bucket: organization.gcs_bucket_name, object_path: node.desired_state_object_path)
    assert_equal "docker.io/mccutchen/go-httpbin@sha256:#{'a' * 64}", desired_state_services(desired_state).first.fetch("image")
  end

  test "omits empty secret refs from desired state" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json(services: {
        "web" => web_service_runtime(env: { "RAILS_ENV" => "production" }, secret_refs: [])
      }),
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)
    store = FakeObjectStore.new

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    Deployments::Publisher.new(environment: environment, release: release, store: store).call

    desired_state = store.desired_state_payload(bucket: organization.gcs_bucket_name, object_path: node.desired_state_object_path)
    assert_not desired_state_services(desired_state).first.key?("secretRefs")
  end

  test "publishes web and worker based on node labels" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json(services: {
        "web" => web_service_runtime(port: 80, env: { "RAILS_ENV" => "production" }),
        "worker" => worker_service_runtime(command: ["./bin/jobs"])
      }),
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a", labels: ["web", "worker"])
    node.update!(environment: environment)
    store = FakeObjectStore.new
    hostname = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call)
      .with do
        environment.create_environment_ingress!(
          hostname: hostname,
          cloudflare_tunnel_id: "tunnel-1",
          gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
          status: EnvironmentIngress::STATUS_READY,
          provisioned_at: Time.current
        )
        true
      end
      .returns(true)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    Deployments::Publisher.new(environment: environment, release: release, store: store).call

    desired_state = store.desired_state_payload(bucket: organization.gcs_bucket_name, object_path: node.desired_state_object_path)
    services = desired_state_services(desired_state)
    assert_equal %w[web worker], services.map { |entry| entry.fetch("name") }
    assert_equal "./bin/jobs", services.second.dig("entrypoint", 0)
    assert_equal [ hostname ], desired_state.dig("ingress", "hosts")
  end

  test "renders environment-scoped volume names in desired state" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json(services: {
        "web" => web_service_runtime(volumes: [ { "source" => "app_storage", "target" => "/rails/storage" } ]),
        "worker" => worker_service_runtime(command: ["./bin/jobs"], volumes: [ { "source" => "app_storage", "target" => "/rails/storage" } ])
      }),
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a", labels: ["web", "worker"])
    node.update!(environment: environment)
    store = FakeObjectStore.new
    hostname = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call)
      .with do
        environment.create_environment_ingress!(
          hostname: hostname,
          cloudflare_tunnel_id: "tunnel-1",
          gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
          status: EnvironmentIngress::STATUS_READY,
          provisioned_at: Time.current
        )
        true
      end
      .returns(true)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    Deployments::Publisher.new(environment: environment, release: release, store: store).call

    desired_state = store.desired_state_payload(bucket: organization.gcs_bucket_name, object_path: node.desired_state_object_path)
    mounts = desired_state_services(desired_state).map { |entry| entry.fetch("volumeMounts").first }

    assert_equal [
      { "source" => "devopsellence-env-#{environment.id}-app_storage", "target" => "/rails/storage" },
      { "source" => "devopsellence-env-#{environment.id}-app_storage", "target" => "/rails/storage" }
    ], mounts
  end

  test "merges managed environment secrets into published desired state" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    environment.environment_secrets.create!(service_name: "web", name: "SECRET_KEY_BASE")
    hostname = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"
    environment.create_environment_ingress!(
      hostname: hostname,
      cloudflare_tunnel_id: "tunnel-1",
      gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json(services: {
        "web" => web_service_runtime(
          port: 80,
          env: { "RAILS_ENV" => "production" },
          secret_refs: [ { "name" => "DATABASE_URL", "secret" => "projects/acme/secrets/db/versions/latest" } ]
        )
      }),
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a", labels: ["web"])
    node.update!(environment: environment)
    store = FakeObjectStore.new

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_environment_access!).returns(true)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    Deployments::Publisher.new(environment: environment, release: release, store: store).call

    desired_state = store.desired_state_payload(bucket: organization.gcs_bucket_name, object_path: node.desired_state_object_path)
    assert_equal(
      {
        "DATABASE_URL" => "projects/acme/secrets/db/versions/latest",
        "SECRET_KEY_BASE" => environment.environment_secrets.first.secret_ref
      },
      desired_state_services(desired_state).first.fetch("secretRefs")
    )
    assert_equal [ hostname ], desired_state.dig("ingress", "hosts")
  end

  test "provisions ingress before publishing web releases" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json,
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)
    provisioned = false
    hostname = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call)
      .with do
        provisioned = true
        environment.create_environment_ingress!(
          hostname: hostname,
          cloudflare_tunnel_id: "tunnel-1",
          gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
          status: EnvironmentIngress::STATUS_READY,
          provisioned_at: Time.current
        )
        true
      end
      .returns(true)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    Deployments::Publisher.new(environment: environment, release: release, store: FakeObjectStore.new).call

    assert_equal true, provisioned
  end

  test "publishes ingress created during provisioning on the first rollout" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json,
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)
    store = FakeObjectStore.new
    hostname = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call)
      .with do
        environment.create_environment_ingress!(
          hostname: hostname,
          cloudflare_tunnel_id: "tunnel-1",
          gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
          status: EnvironmentIngress::STATUS_READY,
          provisioned_at: Time.current
        )
        true
      end
      .returns(true)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)

    Deployments::Publisher.new(environment: environment, release: release, store: store).call

    desired_state = store.desired_state_payload(bucket: organization.gcs_bucket_name, object_path: node.desired_state_object_path)
    assert_equal [ hostname ], desired_state.dig("ingress", "hosts")
    assert_equal Environment::INGRESS_STRATEGY_TUNNEL, desired_state.dig("ingress", "mode")
  end

  test "runs ingress provisioning and desired state publish outside the release transaction" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json,
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a")
    node.update!(environment: environment)

    baseline_transactions = ApplicationRecord.connection.open_transactions
    ingress_transactions = []
    publish_transactions = []

    EnvironmentIngresses::Reconciler.any_instance.stubs(:call)
      .with do
        ingress_transactions << ApplicationRecord.connection.open_transactions
        environment.create_environment_ingress!(
          hostname: "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io",
          cloudflare_tunnel_id: "tunnel-1",
          gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
          status: EnvironmentIngress::STATUS_READY,
          provisioned_at: Time.current
        )
        true
      end
      .returns(true)
    Nodes::DesiredStatePublisher.any_instance.stubs(:call)
      .with do
        publish_transactions << ApplicationRecord.connection.open_transactions
        true
      end
      .returns(Nodes::DesiredStatePublisher::Result.new(sequence: 1, uri: "gs://example/desired-state.json", payload: {}))
    Deployments::Publisher.new(environment: environment, release: release, store: FakeObjectStore.new).call

    assert_equal [ baseline_transactions ], ingress_transactions
    assert_equal [ baseline_transactions ], publish_transactions
  end

  test "rejects stateful release across multiple nodes" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json(services: {
        "web" => web_service_runtime(port: 80, volumes: [ { "source" => "app_storage", "target" => "/rails/storage" } ])
      }),
      revision: "rel-1"
    )
    node_a, _access_a, _refresh_a = issue_test_node!(organization: organization, name: "node-a")
    node_b, _access_b, _refresh_b = issue_test_node!(organization: organization, name: "node-b")
    node_a.update!(environment: environment)
    node_b.update!(environment: environment)
    hostname = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"
    environment.create_environment_ingress!(
      hostname: hostname,
      cloudflare_tunnel_id: "tunnel-1",
      gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    error = assert_raises(Deployments::Publisher::SchedulingError) do
      Deployments::Publisher.new(environment: environment, release: release, store: FakeObjectStore.new).call
    end

    assert_match "stateful releases", error.message
  end

  test "rejects direct_dns releases when assigned web nodes lack capability" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES,
      ingress_strategy: Environment::INGRESS_STRATEGY_DIRECT_DNS
    )
    environment.create_environment_ingress!(
      hostname: random_ingress_hostname,
      gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json(services: {
        "web" => web_service_runtime(port: 80)
      }),
      revision: "rel-1"
    )
    node, _access, _refresh = issue_test_node!(organization: organization, name: "node-a", labels: ["web"])
    node.update!(environment: environment)

    error = assert_raises(Deployments::Publisher::SchedulingError) do
      Deployments::Publisher.new(environment: environment, release: release, store: FakeObjectStore.new).call
    end

    assert_equal "assigned ingress nodes do not support direct_dns ingress: node-a", error.message
  end

  test "direct_dns desired state includes other node peers" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES,
      ingress_strategy: Environment::INGRESS_STRATEGY_DIRECT_DNS
    )
    hostname = random_ingress_hostname
    environment.create_environment_ingress!(
      hostname: hostname,
      gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )
    release = project.releases.create!(
      git_sha: "abcd1234",
      image_digest: "sha256:abc",
      image_repository: "api",
      runtime_json: release_runtime_json(services: {
        "web" => web_service_runtime(port: 80)
      }),
      revision: "rel-1"
    )
    node_a, = issue_test_node!(organization: organization, name: "node-a", labels: ["web"], public_ip: "198.51.100.10")
    node_b, = issue_test_node!(organization: organization, name: "node-b", labels: ["web"], public_ip: "198.51.100.11")
    worker, = issue_test_node!(organization: organization, name: "worker-a", labels: ["worker"], public_ip: "198.51.100.12")
    [ node_a, node_b, worker ].each do |node|
      node.capabilities = [ Node::CAPABILITY_DIRECT_DNS_INGRESS ]
      node.update!(environment: environment)
    end

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)

    store = FakeObjectStore.new
    Deployments::Publisher.new(environment: environment, release: release, store: store).call

    state_a = store.desired_state_payload(bucket: organization.gcs_bucket_name, object_path: node_a.reload.desired_state_object_path)
    state_b = store.desired_state_payload(bucket: organization.gcs_bucket_name, object_path: node_b.reload.desired_state_object_path)
    assert_equal [ hostname ], state_a.dig("ingress", "hosts")

    assert_equal [ "node-b", "worker-a" ], state_a.fetch("nodePeers").map { |peer| peer.fetch("name") }
    node_b_peer = state_a.fetch("nodePeers").find { |peer| peer.fetch("name") == "node-b" }
    assert_equal [ "web" ], node_b_peer.fetch("labels")
    assert_equal "198.51.100.11", node_b_peer.fetch("publicAddress")

    assert_equal [ "198.51.100.10" ], state_b.fetch("nodePeers").select { |peer| peer.fetch("labels").include?("web") }.map { |peer| peer.fetch("publicAddress") }
  end

  test "managed deploy claims a node bundle for a new environment" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project     = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name:                       "Production",
      gcp_project_id:             organization.gcp_project_id,
      gcp_project_number:         organization.gcp_project_number,
      workload_identity_pool:     organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind:               Environment::RUNTIME_MANAGED,
      runtime_project:            RuntimeProject.default!
    )
    release = project.releases.create!(
      git_sha:          "abcd1234",
      image_digest:     "sha256:#{"a" * 64}",
      image_repository: "api",
      runtime_json:     release_runtime_json,
      revision:         "rel-1"
    )
    bundle_node, = issue_test_node!(
      organization: nil, name: "warm-node-env",
      managed: true, managed_provider: "hetzner",
      managed_region: "ash", managed_size_slug: "cpx11",
      provider_server_id: "server-warm-env"
    )
    runtime  = RuntimeProject.default!
    hostname = random_ingress_hostname
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      gcs_bucket_name: organization.gcs_bucket_name,
      gar_repository_name: organization.gar_repository_name,
      gar_repository_region: organization.gar_repository_region,
      gar_writer_service_account_email: "ob#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_CLAIMED,
      claimed_by_organization: organization
    )
    organization.update!(organization_bundle: organization_bundle)
    environment_bundle = EnvironmentBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      claimed_by_environment: environment,
      service_account_email: "env-bsa@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      gcp_secret_name: "eb-env-tunnel",
      hostname: hostname,
      cloudflare_tunnel_id: "tunnel-env",
      status: EnvironmentBundle::STATUS_CLAIMED,
      provisioned_at: 30.minutes.ago
    )
    environment.update!(environment_bundle: environment_bundle, service_account_email: environment_bundle.service_account_email)
    warm_bundle = NodeBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      environment_bundle: environment_bundle,
      status: NodeBundle::STATUS_WARM,
      provisioned_at: 30.minutes.ago
    )
    store = FakeObjectStore.new

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    with_object_store(store) do
      result = Deployments::Publisher.new(environment: environment, release: release, store: store).call
      assert_equal 1, result.assigned_nodes
    end

    assert_equal environment.id, bundle_node.reload.environment_id
    assert_equal organization.id, bundle_node.reload.organization_id
    assert_equal warm_bundle.id, bundle_node.node_bundle_id
    assert_equal release.id, environment.reload.current_release_id
    assert_equal hostname, environment.reload.environment_ingress.hostname
    assert_predicate bundle_node.reload.desired_state_bucket, :present?
    assert_predicate bundle_node.reload.desired_state_object_path, :present?
  end

  test "managed deploy claims a node bundle and publishes desired state" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project     = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name:                       "Production",
      gcp_project_id:             organization.gcp_project_id,
      gcp_project_number:         organization.gcp_project_number,
      workload_identity_pool:     organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind:               Environment::RUNTIME_MANAGED,
      runtime_project:            RuntimeProject.default!
    )
    release = project.releases.create!(
      git_sha:          "abcd1234",
      image_digest:     "sha256:#{"a" * 64}",
      image_repository: "api",
      runtime_json:     release_runtime_json,
      revision:         "rel-1"
    )
    bundle_node, = issue_test_node!(
      organization: nil, name: "warm-node",
      managed: true, managed_provider: "hetzner",
      managed_region: "ash", managed_size_slug: "cpx11",
      provider_server_id: "server-warm"
    )
    runtime  = RuntimeProject.default!
    hostname = random_ingress_hostname
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      gcs_bucket_name: organization.gcs_bucket_name,
      gar_repository_name: organization.gar_repository_name,
      gar_repository_region: organization.gar_repository_region,
      gar_writer_service_account_email: "ob#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_CLAIMED,
      claimed_by_organization: organization
    )
    organization.update!(organization_bundle: organization_bundle)
    environment_bundle = EnvironmentBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      claimed_by_environment: environment,
      service_account_email: "bsa@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      gcp_secret_name: "eb-mgd-tunnel",
      hostname: hostname,
      cloudflare_tunnel_id: "tunnel-mgd",
      status: EnvironmentBundle::STATUS_CLAIMED,
      provisioned_at: 30.minutes.ago
    )
    environment.update!(environment_bundle: environment_bundle, service_account_email: environment_bundle.service_account_email)
    warm_bundle = NodeBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      environment_bundle: environment_bundle,
      status: NodeBundle::STATUS_WARM,
      provisioned_at: 30.minutes.ago
    )
    store = FakeObjectStore.new

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    with_object_store(store) do
      result = Deployments::Publisher.new(environment: environment, release: release, store: store).call
      assert_equal 1, result.assigned_nodes
    end

    assert_equal environment.id, bundle_node.reload.environment_id
    assert_equal organization.id, bundle_node.reload.organization_id
    assert_equal [ "web" ], bundle_node.labels
    assert_equal warm_bundle.id, bundle_node.node_bundle_id
    assert_equal hostname, environment.reload.environment_ingress.hostname
    assert_predicate bundle_node.reload.desired_state_bucket, :present?
    assert_predicate bundle_node.reload.desired_state_object_path, :present?
    assert_equal 1, bundle_node.reload.desired_state_sequence
    assert_equal 1, warm_bundle.reload.desired_state_sequence
    writes_for_bundle = store.writes.select { |entry| entry[:bucket] == bundle_node.desired_state_bucket && entry[:object_path] == bundle_node.desired_state_object_path }
    assert_equal 1, writes_for_bundle.size
  end

  test "skips ingress provisioning when environment already has a ready ingress (bundle-backed path)" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project     = organization.projects.create!(name: "BundleApp")
    environment = project.environments.create!(
      name:                       "production",
      gcp_project_id:             organization.gcp_project_id,
      gcp_project_number:         organization.gcp_project_number,
      workload_identity_pool:     organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind:               Environment::RUNTIME_CUSTOMER_NODES
    )
    ensure_test_environment_bundle!(environment)
    hostname = random_ingress_hostname
    environment.create_environment_ingress!(
      hostname:             hostname,
      cloudflare_tunnel_id: "tunnel-pre",
      gcp_secret_name:      "wb-pre-tunnel",
      status:               EnvironmentIngress::STATUS_READY,
      provisioned_at:       1.hour.ago
    )
    release = project.releases.create!(
      git_sha:          "a" * 40,
      revision:         "r-bundle",
      image_repository: "bundle-app",
      image_digest:     "sha256:#{"b" * 64}",
      runtime_json:     release_runtime_json
    )
    node, _, _ = issue_test_node!(organization: organization, name: "pre-node")
    node.update!(environment: environment)

    ingress_provisioner_called = false

    Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
      Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
    )
    Gcp::EnvironmentSecretManager.any_instance.stubs(:ensure_ingress_access!).returns(true)
    Cloudflare::EnvironmentIngressProvisioner.any_instance.expects(:call).never
    Deployments::Publisher.new(
      environment: environment,
      release:     release,
      store:       FakeObjectStore.new
    ).call

    assert_equal false, ingress_provisioner_called, "CloudflareIngressProvisioner must not be called when ingress is already ready"
    environment.reload
    assert_equal hostname, environment.environment_ingress.hostname
  end

  test "publishing desired state does not re-run environment IAM repair on the hot deploy path" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "FastPath")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    ensure_test_environment_bundle!(environment)
    environment.create_environment_ingress!(
      hostname: random_ingress_hostname,
      cloudflare_tunnel_id: "tunnel-fast",
      gcp_secret_name: "wb-fast-tunnel",
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: 1.hour.ago
    )
    environment.environment_secrets.create!(
      name: "DATABASE_URL",
      service_name: "web",
      gcp_secret_name: "env-fast-db"
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "r-fast",
      image_repository: "fast-app",
      image_digest: "sha256:#{"b" * 64}",
      runtime_json: release_runtime_json(services: {
        "web" => web_service_runtime(secret_refs: [ { "name" => "DATABASE_URL", "secret" => "projects/acme/secrets/db/versions/latest" } ])
      })
    )
    node, = issue_test_node!(organization: organization, name: "fast-node")
    node.update!(environment: environment)

    Gcp::EnvironmentRuntimeProvisioner.any_instance.expects(:call).never
    Gcp::EnvironmentSecretManager.any_instance.expects(:ensure_environment_access!).never
    Gcp::EnvironmentSecretManager.any_instance.expects(:ensure_ingress_access!).never

    Deployments::Publisher.new(
      environment: environment,
      release: release,
      store: FakeObjectStore.new
    ).call
  end

  private

  def deployment_statuses_for(environment)
    DeploymentNodeStatus.joins(:deployment).where(deployments: { environment_id: environment.id }).order(:id).to_a
  end

  def desired_state_environment(desired_state)
    desired_state.fetch("environments").first
  end

  def desired_state_services(desired_state)
    desired_state_environment(desired_state).fetch("services")
  end

  def desired_state_tasks(desired_state)
    desired_state_environment(desired_state).fetch("tasks")
  end
end
