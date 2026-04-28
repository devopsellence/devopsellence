# frozen_string_literal: true

require "test_helper"
require "securerandom"

class GcpRuntimeProvisionersTest < ActiveSupport::TestCase
  TestResponse = Struct.new(:code, :body, keyword_init: true)

  test "organization runtime provisioner claims a warm organization bundle and syncs identifiers" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    runtime = Devopsellence::RuntimeConfig.current
    bundle = OrganizationBundle.create!(
      runtime_project: organization.active_runtime_project,
      gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-ob-#{SecureRandom.hex(3)}",
      gar_repository_name: "ob-#{SecureRandom.hex(3)}-apps",
      gar_repository_region: runtime.gar_region,
      gar_writer_service_account_email: "ob#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_WARM
    )

    result = Gcp::OrganizationRuntimeProvisioner.new(organization: organization).call

    organization.reload
    assert_equal Organization::PROVISIONING_READY, result.status
    assert_equal bundle, organization.organization_bundle
    assert_equal runtime.gcp_project_id, organization.gcp_project_id
    assert_equal runtime.gcp_project_number, organization.gcp_project_number
    assert_equal runtime.workload_identity_pool, organization.workload_identity_pool
    assert_equal runtime.workload_identity_provider, organization.workload_identity_provider
    assert_equal bundle.gar_repository_region, organization.gar_repository_region
    assert_equal bundle.gcs_bucket_name, organization.gcs_bucket_name
    assert_equal bundle.gar_repository_name, organization.gar_repository_name
  end

  test "environment runtime provisioner claims a warm environment bundle and syncs ingress" do
    runtime_project = RuntimeProject.create!(
      name: "Runtime A",
      slug: "runtime-a-#{SecureRandom.hex(3)}",
      kind: RuntimeProject::KIND_SHARED_SANDBOX,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      gar_region: "us-east1",
      gcs_bucket_prefix: "bucket"
    )
    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime_project,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      gcs_bucket_name: "bucket-a",
      gar_repository_name: "repo-a",
      gar_repository_region: "us-east1",
      provisioning_status: Organization::PROVISIONING_READY
    )
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime_project,
      claimed_by_organization: organization,
      claimed_at: Time.current,
      gcs_bucket_name: "bucket-a",
      gar_repository_name: "repo-a",
      gar_repository_region: "us-east1",
      gar_writer_service_account_email: "writer-a@gcp-proj-a.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_CLAIMED
    )
    organization.update!(organization_bundle: organization_bundle)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_project: organization.runtime_project
    )
    bundle = EnvironmentBundle.create!(
      runtime_project: runtime_project,
      organization_bundle: organization_bundle,
      service_account_email: "env-bundle-a@gcp-proj-a.iam.gserviceaccount.com",
      hostname: "env-a.devopsellence.test",
      status: EnvironmentBundle::STATUS_WARM
    )

    result = Gcp::EnvironmentRuntimeProvisioner.new(environment: environment).call

    assert_equal :ready, result.status
    assert_equal bundle, environment.reload.environment_bundle
    assert_equal bundle.service_account_email, environment.service_account_email
    assert_equal bundle.hostname, environment.environment_ingress.hostname
  end

  test "environment runtime provisioner provisions and claims a bundle when none are warm" do
    runtime_project = RuntimeProject.create!(
      name: "Runtime B",
      slug: "runtime-b-#{SecureRandom.hex(3)}",
      kind: RuntimeProject::KIND_SHARED_SANDBOX,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      gar_region: "us-east1",
      gcs_bucket_prefix: "bucket"
    )
    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime_project,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      gcs_bucket_name: "bucket-a",
      gar_repository_name: "repo-a",
      gar_repository_region: "us-east1",
      provisioning_status: Organization::PROVISIONING_READY
    )
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime_project,
      claimed_by_organization: organization,
      claimed_at: Time.current,
      gcs_bucket_name: "bucket-a",
      gar_repository_name: "repo-a",
      gar_repository_region: "us-east1",
      gar_writer_service_account_email: "writer-b@gcp-proj-a.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_CLAIMED
    )
    organization.update!(organization_bundle: organization_bundle)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      service_account_email: nil,
      runtime_kind: Environment::RUNTIME_MANAGED
    )

    fake_bundle = EnvironmentBundle.create!(
      runtime_project: organization_bundle.runtime_project,
      organization_bundle: organization_bundle,
      service_account_email: "new-env-bundle@gcp-proj-a.iam.gserviceaccount.com",
      hostname: "new-env.devopsellence.test",
      status: EnvironmentBundle::STATUS_WARM
    )
    EnvironmentBundles::Provisioner.any_instance.stubs(:call).returns(fake_bundle)
    result = Gcp::EnvironmentRuntimeProvisioner.new(environment: environment).call

    assert_equal :ready, result.status
    assert_equal fake_bundle, environment.reload.environment_bundle
    assert_equal "new-env-bundle@gcp-proj-a.iam.gserviceaccount.com", environment.service_account_email
    assert_equal "new-env.devopsellence.test", environment.environment_ingress.hostname
  end

  test "environment runtime provisioner does not use warm env pool for a project's second environment" do
    runtime_project = RuntimeProject.create!(
      name: "Runtime C",
      slug: "runtime-c-#{SecureRandom.hex(3)}",
      kind: RuntimeProject::KIND_SHARED_SANDBOX,
      gcp_project_id: "gcp-proj-c",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-c",
      workload_identity_provider: "provider-c",
      gar_region: "us-east1",
      gcs_bucket_prefix: "bucket"
    )
    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime_project,
      gcp_project_id: "gcp-proj-c",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-c",
      workload_identity_provider: "provider-c",
      gcs_bucket_name: "bucket-c",
      gar_repository_name: "repo-c",
      gar_repository_region: "us-east1",
      provisioning_status: Organization::PROVISIONING_READY
    )
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime_project,
      claimed_by_organization: organization,
      claimed_at: Time.current,
      gcs_bucket_name: "bucket-c",
      gar_repository_name: "repo-c",
      gar_repository_region: "us-east1",
      gar_writer_service_account_email: "writer-c@gcp-proj-c.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_CLAIMED
    )
    organization.update!(organization_bundle: organization_bundle)
    project = organization.projects.create!(name: "Project C")
    existing_environment = project.environments.create!(
      name: "Production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_project: organization.runtime_project
    )
    existing_bundle = EnvironmentBundle.create!(
      runtime_project: runtime_project,
      organization_bundle: organization_bundle,
      claimed_by_environment: existing_environment,
      claimed_at: Time.current,
      service_account_email: "env-bundle-existing@gcp-proj-c.iam.gserviceaccount.com",
      hostname: "existing.devopsellence.test",
      status: EnvironmentBundle::STATUS_CLAIMED
    )
    existing_environment.update!(environment_bundle: existing_bundle)

    warm_bundle = EnvironmentBundle.create!(
      runtime_project: runtime_project,
      organization_bundle: organization_bundle,
      service_account_email: "env-bundle-warm@gcp-proj-c.iam.gserviceaccount.com",
      hostname: "warm.devopsellence.test",
      status: EnvironmentBundle::STATUS_WARM
    )

    new_environment = project.environments.create!(
      name: "Staging",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_MANAGED
    )

    provisioned_bundle = EnvironmentBundle.create!(
      runtime_project: organization_bundle.runtime_project,
      organization_bundle: organization_bundle,
      service_account_email: "new-env-bundle@gcp-proj-c.iam.gserviceaccount.com",
      hostname: "new-env.devopsellence.test",
      status: EnvironmentBundle::STATUS_WARM
    )
    EnvironmentBundles::Provisioner.any_instance.stubs(:call).returns(provisioned_bundle)
    result = Gcp::EnvironmentRuntimeProvisioner.new(environment: new_environment).call

    assert_equal :ready, result.status
    assert_equal provisioned_bundle, new_environment.reload.environment_bundle
    assert_equal EnvironmentBundle::STATUS_WARM, warm_bundle.reload.status
    assert_nil warm_bundle.claimed_by_environment
  end

  test "gar push auth grants token creator on org writer service account to control plane runtime" do
    runtime = RuntimeProject.default!
    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider,
      gar_repository_region: runtime.gar_region,
      gar_repository_name: "org-123-apps",
      gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-org-123",
      provisioning_status: Organization::PROVISIONING_READY
    )
    ensure_test_organization_bundle!(organization, runtime:)

    iam = Object.new
    iam.stubs(:get_project_service_account).returns(true)

    client = Class.new do
      attr_reader :repository_set_policy_calls, :service_account_policy, :token_requests

      def initialize
        @repository_set_policy_calls = 0
        @service_account_policy = nil
        @token_requests = []
      end

      def get(uri)
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}") if uri.include?(":getIamPolicy")

        raise "unexpected uri: #{uri}"
      end

      def post(uri, payload:)
        if uri.include?("serviceAccounts/") && uri.include?(":getIamPolicy")
          return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}")
        end

        if uri.include?("serviceAccounts/") && uri.include?(":setIamPolicy")
          @service_account_policy = payload.fetch(:policy)
          return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload))
        end

        if uri.include?(":setIamPolicy")
          @repository_set_policy_calls += 1
          return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload))
        end

        if uri.include?(":generateAccessToken")
          @token_requests << [uri, payload]
          return GcpRuntimeProvisionersTest::TestResponse.new(
            code: "200",
            body: "{\"accessToken\":\"ya29.fake\",\"expireTime\":\"2030-01-01T00:00:00Z\"}"
          )
        end

        raise "unexpected uri: #{uri}"
      end
    end.new

    auth = nil
    with_env(
      "DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_ID" => "devopsellence-control-plane",
      "DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_EMAIL" => "devopsellence-control-plane@#{runtime.gcp_project_id}.iam.gserviceaccount.com"
    ) do
      auth = Runtime::Broker::LocalClient.new(client:, iam:, retry_sleep_seconds: 0).issue_gar_push_auth!(organization:)
    end

    assert_equal "oauth2accesstoken", auth.docker_username
    assert_equal "ya29.fake", auth.docker_password
    assert_equal 1, client.repository_set_policy_calls
    assert_equal 1, client.token_requests.length

    token_creator_binding = Array(client.service_account_policy&.fetch("bindings", [])).find { |binding| binding["role"] == "roles/iam.serviceAccountTokenCreator" }
    assert token_creator_binding
    assert_includes token_creator_binding["members"], "serviceAccount:devopsellence-control-plane@#{runtime.gcp_project_id}.iam.gserviceaccount.com"
  end

  test "gar push auth retries service account policy fetch until iam propagation completes" do
    runtime = RuntimeProject.default!
    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider,
      gar_repository_region: runtime.gar_region,
      gar_repository_name: "org-#{SecureRandom.hex(3)}-apps",
      gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-org-#{SecureRandom.hex(3)}",
      provisioning_status: Organization::PROVISIONING_READY
    )
    ensure_test_organization_bundle!(organization, runtime:)

    client = Class.new do
      attr_reader :service_account_get_policy_calls

      def initialize
        @service_account_get_policy_calls = 0
      end

      def get(uri)
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}") if uri.include?(":getIamPolicy")

        raise "unexpected uri: #{uri}"
      end

      def post(uri, payload:)
        if uri.include?("serviceAccounts/") && uri.include?(":getIamPolicy")
          @service_account_get_policy_calls += 1
          return GcpRuntimeProvisionersTest::TestResponse.new(
            code: "403",
            body: "{\"error\":{\"status\":\"PERMISSION_DENIED\",\"message\":\"Permission 'iam.serviceAccounts.getIamPolicy' denied\"}}"
          ) if @service_account_get_policy_calls < 10

          return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}")
        end

        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload)) if uri.include?(":setIamPolicy")

        if uri.include?(":generateAccessToken")
          return GcpRuntimeProvisionersTest::TestResponse.new(
            code: "200",
            body: "{\"accessToken\":\"ya29.retry-policy\",\"expireTime\":\"2030-01-01T00:00:00Z\"}"
          )
        end

        raise "unexpected uri: #{uri}"
      end
    end.new

    auth = nil
    with_env(
      "DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_ID" => "devopsellence-control-plane",
      "DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_EMAIL" => "devopsellence-control-plane@#{runtime.gcp_project_id}.iam.gserviceaccount.com"
    ) do
      auth = Runtime::Broker::LocalClient.new(client:, retry_sleep_seconds: 0).issue_gar_push_auth!(organization:)
    end

    assert_equal "ya29.retry-policy", auth.docker_password
    assert_equal 10, client.service_account_get_policy_calls
  end

  test "runtime project audience accepts full workload identity resource names" do
    runtime = RuntimeProject.create!(
      name: "Runtime Full Names",
      slug: "runtime-full-#{SecureRandom.hex(3)}",
      kind: RuntimeProject::KIND_SHARED_SANDBOX,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: "projects/123456789/locations/global/workloadIdentityPools/devopsellence-nodes",
      workload_identity_provider: "projects/123456789/locations/global/workloadIdentityPools/devopsellence-nodes/providers/devopsellence-nodes",
      gar_region: "us-east1",
      gcs_bucket_prefix: "bucket"
    )

    assert_equal "projects/123456789/locations/global/workloadIdentityPools/devopsellence-nodes", runtime.workload_identity_pool_resource_name
    assert_equal "projects/123456789/locations/global/workloadIdentityPools/devopsellence-nodes/providers/devopsellence-nodes", runtime.workload_identity_provider_resource_name
    assert_equal "//iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/devopsellence-nodes/providers/devopsellence-nodes", runtime.audience
  end

  test "runtime project audience rejects short workload identity ids" do
    runtime = RuntimeProject.create!(
      name: "Runtime Short Names",
      slug: "runtime-short-#{SecureRandom.hex(3)}",
      kind: RuntimeProject::KIND_SHARED_SANDBOX,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: "devopsellence-nodes",
      workload_identity_provider: "devopsellence-nodes",
      gar_region: "us-east1",
      gcs_bucket_prefix: "bucket"
    )

    error = assert_raises(ArgumentError) { runtime.audience }
    assert_includes error.message, "full workload identity"
  end

  test "gar push auth prefers discovered runtime service account email over tenant project fallback" do
    runtime = RuntimeProject.default!
    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider,
      gar_repository_region: runtime.gar_region,
      gar_repository_name: "org-meta-apps",
      gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-org-meta",
      provisioning_status: Organization::PROVISIONING_READY
    )
    ensure_test_organization_bundle!(organization, runtime:)

    iam = Object.new
    iam.stubs(:get_project_service_account).returns(true)

    client = Class.new do
      attr_reader :service_account_policy

      def initialize
        @service_account_policy = nil
      end

      def get(uri)
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}") if uri.include?(":getIamPolicy")

        raise "unexpected uri: #{uri}"
      end

      def post(uri, payload:)
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}") if uri.include?("serviceAccounts/") && uri.include?(":getIamPolicy")
        if uri.include?("serviceAccounts/") && uri.include?(":setIamPolicy")
          @service_account_policy = payload.fetch(:policy)
          return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload))
        end
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload)) if uri.include?(":setIamPolicy")

        if uri.include?(":generateAccessToken")
          return GcpRuntimeProvisionersTest::TestResponse.new(
            code: "200",
            body: "{\"accessToken\":\"ya29.fake\",\"expireTime\":\"2030-01-01T00:00:00Z\"}"
          )
        end

        raise "unexpected uri: #{uri}"
      end
    end.new

    broker = Runtime::Broker::LocalClient.new(client:, iam:, retry_sleep_seconds: 0)
    broker.stubs(:current_service_account_email).returns("devopsellence-control-plane@runtime-prod-example.iam.gserviceaccount.com")
    broker.issue_gar_push_auth!(organization:)

    token_creator_binding = Array(client.service_account_policy&.fetch("bindings", [])).find { |binding| binding["role"] == "roles/iam.serviceAccountTokenCreator" }
    assert token_creator_binding
    assert_includes token_creator_binding["members"], "serviceAccount:devopsellence-control-plane@runtime-prod-example.iam.gserviceaccount.com"
  end

  test "node bundle impersonation accepts full workload identity resource names" do
    runtime = RuntimeProject.create!(
      name: "Runtime Node Pool",
      slug: "runtime-node-#{SecureRandom.hex(3)}",
      kind: RuntimeProject::KIND_SHARED_SANDBOX,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      workload_identity_pool: "projects/123456789/locations/global/workloadIdentityPools/devopsellence-nodes",
      workload_identity_provider: "projects/123456789/locations/global/workloadIdentityPools/devopsellence-nodes/providers/devopsellence-nodes",
      gar_region: "us-east1",
      gcs_bucket_prefix: "bucket"
    )
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      gcs_bucket_name: "bucket-a",
      gar_repository_name: "repo-a",
      gar_repository_region: "us-east1",
      gar_writer_service_account_email: "writer-a@gcp-proj-a.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_WARM
    )
    environment_bundle = EnvironmentBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      service_account_email: "env-bundle-a@gcp-proj-a.iam.gserviceaccount.com",
      status: EnvironmentBundle::STATUS_WARM
    )
    node_bundle = NodeBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      environment_bundle: environment_bundle
    )

    service_account_policy = Google::Apis::IamV1::Policy.new(bindings: [], version: 1)
    iam = Object.new
    iam.stubs(:get_project_service_account_iam_policy).returns(service_account_policy)
    iam.stubs(:set_service_account_iam_policy)
      .with do |_name, request|
        service_account_policy.bindings = request.policy.bindings
        service_account_policy.version = request.policy.version
        true
      end
      .returns(true)

    client = Class.new do
      attr_reader :service_account_policy

      def initialize
        @service_account_policy = nil
      end

      def post(uri, payload:)
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}") if uri.include?(":getIamPolicy")

        if uri.include?(":setIamPolicy")
          @service_account_policy = payload.fetch(:policy)
          return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload))
        end

        raise "unexpected uri: #{uri}"
      end
    end.new

    result = Runtime::Broker::LocalClient.new(client:, iam:, retry_sleep_seconds: 0).ensure_node_bundle_impersonation!(bundle: node_bundle)

    assert_equal :ready, result.status
    binding = Array(client.service_account_policy&.fetch("bindings", [])).find { |entry| entry["role"] == "roles/iam.workloadIdentityUser" }
    assert binding
    assert_includes binding["members"], "principal://iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/devopsellence-nodes/subject/node_bundle:#{node_bundle.token}"
  end

  test "gar push auth retries token generation until iam propagation completes" do
    runtime = RuntimeProject.default!
    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider,
      gar_repository_region: runtime.gar_region,
      gar_repository_name: "org-456-apps",
      gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-org-456",
      provisioning_status: Organization::PROVISIONING_READY
    )
    ensure_test_organization_bundle!(organization, runtime:)

    service_account_policy = Google::Apis::IamV1::Policy.new(bindings: [], version: 1)
    iam = Object.new
    iam.stubs(:get_project_service_account).returns(true)
    iam.stubs(:get_project_service_account_iam_policy).returns(service_account_policy)
    iam.stubs(:set_service_account_iam_policy)
      .with do |_name, request|
        service_account_policy.bindings = request.policy.bindings
        service_account_policy.version = request.policy.version
        true
      end
      .returns(true)

    client = Class.new do
      attr_reader :token_requests

      def initialize
        @token_requests = 0
      end

      def get(uri)
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}") if uri.include?(":getIamPolicy")

        raise "unexpected uri: #{uri}"
      end

      def post(uri, payload:)
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}") if uri.include?("serviceAccounts/") && uri.include?(":getIamPolicy")
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload)) if uri.include?(":setIamPolicy")

        if uri.include?(":generateAccessToken")
          @token_requests += 1
          return GcpRuntimeProvisionersTest::TestResponse.new(
            code: "403",
            body: "{\"error\":{\"status\":\"PERMISSION_DENIED\",\"message\":\"Permission 'iam.serviceAccounts.getAccessToken' denied\"}}"
          ) if @token_requests < 10

          return GcpRuntimeProvisionersTest::TestResponse.new(
            code: "200",
            body: "{\"accessToken\":\"ya29.retry\",\"expireTime\":\"2030-01-01T00:00:00Z\"}"
          )
        end

        raise "unexpected uri: #{uri}"
      end
    end.new

    auth = nil
    with_env(
      "DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_ID" => "devopsellence-control-plane",
      "DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_EMAIL" => "devopsellence-control-plane@#{runtime.gcp_project_id}.iam.gserviceaccount.com"
    ) do
      auth = Runtime::Broker::LocalClient.new(client:, iam:, retry_sleep_seconds: 0).issue_gar_push_auth!(organization:)
    end

    assert_equal "ya29.retry", auth.docker_password
    assert_equal 10, client.token_requests
  end

  test "environment bundle provisioning refetches bucket iam policy after etag conflict" do
    runtime = RuntimeProject.default!
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-ob-#{SecureRandom.hex(3)}",
      gar_repository_name: "ob-#{SecureRandom.hex(3)}-apps",
      gar_repository_region: runtime.gar_region,
      gar_writer_service_account_email: "ob#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_WARM
    )
    bundle = EnvironmentBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      service_account_email: "eb#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      hostname: "eb-#{SecureRandom.hex(3)}.devopsellence.test",
      status: EnvironmentBundle::STATUS_PROVISIONING
    )

    iam = Object.new
    iam.stubs(:get_project_service_account).returns(true)

    client = Class.new do
      attr_reader :bucket_puts

      def initialize
        @bucket_puts = 0
      end

      def get(uri)
        if uri.include?("/iam")
          return GcpRuntimeProvisionersTest::TestResponse.new(
            code: "200",
            body: "{\"bindings\":[],\"etag\":\"etag-#{@bucket_puts}\"}"
          )
        end

        if uri.include?(":getIamPolicy")
          return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}")
        end

        raise "unexpected uri: #{uri}"
      end

      def put(uri, payload:)
        raise "unexpected uri: #{uri}" unless uri.include?("/iam")

        @bucket_puts += 1
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "412", body: "{\"error\":{\"message\":\"conditionNotMet\"}}") if @bucket_puts == 1

        GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload))
      end

      def post(uri, payload:)
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload)) if uri.include?(":setIamPolicy")

        raise "unexpected uri: #{uri}"
      end
    end.new

    result = Runtime::Broker::LocalClient.new(client:, iam:, retry_sleep_seconds: 0).provision_environment_bundle!(bundle:)

    assert_equal :ready, result.status
    assert_equal 2, client.bucket_puts
  end

  test "environment runtime retries bucket iam update until service account propagation completes" do
    runtime = RuntimeProject.default!
    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider,
      gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-org-#{SecureRandom.hex(3)}",
      gar_repository_name: "org-#{SecureRandom.hex(3)}-apps",
      gar_repository_region: runtime.gar_region,
      provisioning_status: Organization::PROVISIONING_READY
    )
    project = organization.projects.create!(name: "app-#{SecureRandom.hex(3)}")
    environment = project.environments.create!(
      name: "production",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider,
      service_account_email: "eb#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com"
    )

    client = Class.new do
      attr_reader :bucket_puts

      def initialize
        @bucket_puts = 0
      end

      def get(uri)
        if uri.include?("/iam")
          return GcpRuntimeProvisionersTest::TestResponse.new(
            code: "200",
            body: "{\"bindings\":[],\"etag\":\"etag-#{@bucket_puts}\"}"
          )
        end

        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}") if uri.include?(":getIamPolicy")

        raise "unexpected uri: #{uri}"
      end

      def put(uri, payload:)
        raise "unexpected uri: #{uri}" unless uri.include?("/iam")

        @bucket_puts += 1
        member = payload.fetch("bindings").first.fetch("members").first.to_s.sub("serviceAccount:", "")
        return GcpRuntimeProvisionersTest::TestResponse.new(
          code: "400",
          body: JSON.generate(error: { message: "Service account #{member} does not exist." })
        ) if @bucket_puts <= 10

        GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload))
      end

      def post(uri, payload:)
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload)) if uri.include?(":setIamPolicy")

        raise "unexpected uri: #{uri}"
      end
    end.new

    result = Runtime::Broker::LocalClient.new(client:, retry_sleep_seconds: 0).ensure_environment_runtime!(environment:)

    assert_equal :ready, result.status
    assert_equal 11, client.bucket_puts
  end

  test "environment runtime strips deleted members from bucket iam policy before update" do
    runtime = RuntimeProject.default!
    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider,
      gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-org-#{SecureRandom.hex(3)}",
      gar_repository_name: "org-#{SecureRandom.hex(3)}-apps",
      gar_repository_region: runtime.gar_region,
      provisioning_status: Organization::PROVISIONING_READY
    )
    project = organization.projects.create!(name: "app-#{SecureRandom.hex(3)}")
    environment = project.environments.create!(
      name: "production",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider,
      service_account_email: "eb#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com"
    )

    client = Class.new do
      attr_reader :bucket_policy

      def get(uri)
        if uri.include?("/iam")
          return GcpRuntimeProvisionersTest::TestResponse.new(
            code: "200",
            body: JSON.generate(
              "bindings" => [
                {
                  "role" => "roles/storage.objectViewer",
                  "members" => [
                    "deleted:serviceAccount:stale@devopsellence-tenants-prod.iam.gserviceaccount.com?uid=123",
                    "serviceAccount:live@devopsellence-tenants-prod.iam.gserviceaccount.com"
                  ]
                }
              ],
              "etag" => "etag-1"
            )
          )
        end

        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: "{\"bindings\":[]}") if uri.include?(":getIamPolicy")

        raise "unexpected uri: #{uri}"
      end

      def put(uri, payload:)
        raise "unexpected uri: #{uri}" unless uri.include?("/iam")

        @bucket_policy = payload
        GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload))
      end

      def post(uri, payload:)
        return GcpRuntimeProvisionersTest::TestResponse.new(code: "200", body: JSON.generate(payload)) if uri.include?(":setIamPolicy")

        raise "unexpected uri: #{uri}"
      end
    end.new

    result = Runtime::Broker::LocalClient.new(client:, retry_sleep_seconds: 0).ensure_environment_runtime!(environment:)

    assert_equal :ready, result.status
    members = client.bucket_policy.fetch("bindings").find { |binding| binding["role"] == "roles/storage.objectViewer" }.fetch("members")
    assert_equal [
      "serviceAccount:live@devopsellence-tenants-prod.iam.gserviceaccount.com",
      "serviceAccount:#{environment.service_account_email}"
    ], members
  end

  test "environment runtime provisioner returns ready when environment already has a claimed bundle" do
    runtime_project = RuntimeProject.create!(
      name: "Runtime Skip",
      slug: "runtime-skip-#{SecureRandom.hex(3)}",
      kind: RuntimeProject::KIND_SHARED_SANDBOX,
      gcp_project_id: "gcp-proj-skip",
      gcp_project_number: "999000111",
      workload_identity_pool: "pool-skip",
      workload_identity_provider: "provider-skip",
      gar_region: "us-east1",
      gcs_bucket_prefix: "bucket"
    )
    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime_project,
      gcp_project_id: "gcp-proj-skip",
      gcp_project_number: "999000111",
      workload_identity_pool: "pool-skip",
      workload_identity_provider: "provider-skip",
      gcs_bucket_name: "bucket-skip",
      gar_repository_name: "repo-skip",
      gar_repository_region: "us-east1",
      provisioning_status: Organization::PROVISIONING_READY
    )
    project = organization.projects.create!(name: "Project Skip")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_project: runtime_project
    )
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime_project,
      gcs_bucket_name: "bucket-skip",
      gar_repository_name: "repo-skip",
      gar_repository_region: "us-east1",
      gar_writer_service_account_email: "writer-skip@gcp-proj-skip.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_CLAIMED,
      claimed_by_organization: organization,
      claimed_at: Time.current
    )
    environment_bundle = EnvironmentBundle.create!(
      runtime_project: runtime_project,
      organization_bundle: organization_bundle,
      claimed_by_environment: environment,
      claimed_at: Time.current,
      service_account_email: "env-bundle-skip@gcp-proj-skip.iam.gserviceaccount.com",
      hostname: "skip.devopsellence.test",
      status: EnvironmentBundle::STATUS_CLAIMED
    )
    organization.update!(organization_bundle: organization_bundle)
    environment.update!(environment_bundle: environment_bundle, service_account_email: environment_bundle.service_account_email)

    exploding_client = Class.new do
      def post(*)  = raise("unexpected GCP call on already-provisioned environment")
      def get(*)   = raise("unexpected GCP call on already-provisioned environment")
      def put(*)   = raise("unexpected GCP call on already-provisioned environment")
    end.new
    exploding_iam = Class.new do
      def method_missing(*) = raise("unexpected IAM call on already-provisioned environment")
    end.new

    result = Gcp::EnvironmentRuntimeProvisioner.new(
      environment: environment,
      client: exploding_client,
      iam: exploding_iam
    ).call

    assert_equal :ready, result.status
    assert_equal environment_bundle, environment.reload.environment_bundle
  end
end
