# frozen_string_literal: true

require "test_helper"

class MaintenancePruneZombieGcpResourcesTest < ActiveSupport::TestCase
  Response = Struct.new(:code, :body, keyword_init: true)
  Account = Struct.new(:email, keyword_init: true)
  AccountPage = Struct.new(:accounts, :next_page_token, keyword_init: true)

  test "prunes orphan managed gcp resources and keeps active bundle resources" do
    runtime = RuntimeProject.default!
    logger = mock("logger")
    logger.stubs(:info)

    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider,
      gar_repository_region: runtime.gar_region,
      provisioning_status: Organization::PROVISIONING_READY
    )
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      token: "a11111111111",
      claimed_by_organization: organization,
      claimed_at: Time.current,
      status: OrganizationBundle::STATUS_CLAIMED
    )
    organization.update!(
      organization_bundle: organization_bundle,
      gcs_bucket_name: organization_bundle.gcs_bucket_name,
      gar_repository_name: organization_bundle.gar_repository_name,
      gar_repository_region: organization_bundle.gar_repository_region
    )

    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "production",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider
    )
    environment_bundle = EnvironmentBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      token: "a22222222222",
      claimed_by_environment: environment,
      claimed_at: Time.current,
      hostname: "a22222222222.devopsellence.test",
      cloudflare_tunnel_id: "tunnel-a222",
      status: EnvironmentBundle::STATUS_CLAIMED,
      provisioned_at: Time.current
    )
    environment.update!(
      environment_bundle: environment_bundle,
      service_account_email: environment_bundle.service_account_email
    )
    environment.create_environment_ingress!(
      hostname: environment_bundle.hostname,
      gcp_secret_name: environment_bundle.gcp_secret_name,
      status: EnvironmentIngress::STATUS_READY,
      provisioned_at: Time.current
    )
    environment.environment_secrets.create!(
      service_name: "web",
      name: "SECRET_KEY_BASE",
      gcp_secret_name: "env-a22222222222-web-secret-key-base"
    )

    NodeBundle.create!(
      runtime_project: runtime,
      organization_bundle: organization_bundle,
      environment_bundle: environment_bundle,
      token: "a33333333333",
      status: NodeBundle::STATUS_WARM,
      provisioned_at: Time.current
    )

    orphan_bucket_name = "#{runtime.gcs_bucket_prefix}-ob-a44444444444"
    client = FakeClient.new(
      "https://secretmanager.googleapis.com/v1/projects/#{runtime.gcp_project_id}/secrets" => {
        "secrets" => [
          { "name" => "projects/#{runtime.gcp_project_id}/secrets/#{environment_bundle.gcp_secret_name}" },
          { "name" => "projects/#{runtime.gcp_project_id}/secrets/env-a22222222222-web-secret-key-base" },
          { "name" => "projects/#{runtime.gcp_project_id}/secrets/eb-a44444444444-ingress-cloudflare-tunnel-token" },
          { "name" => "projects/#{runtime.gcp_project_id}/secrets/env-a55555555555-web-secret-key-base" },
          { "name" => "projects/#{runtime.gcp_project_id}/secrets/manual-secret" }
        ]
      },
      "https://artifactregistry.googleapis.com/v1/projects/#{runtime.gcp_project_id}/locations/#{runtime.gar_region}/repositories" => {
        "repositories" => [
          { "name" => "projects/#{runtime.gcp_project_id}/locations/#{runtime.gar_region}/repositories/#{organization_bundle.gar_repository_name}" },
          { "name" => "projects/#{runtime.gcp_project_id}/locations/#{runtime.gar_region}/repositories/ob-a44444444444-apps" },
          { "name" => "projects/#{runtime.gcp_project_id}/locations/#{runtime.gar_region}/repositories/manual-repo" }
        ]
      },
      "https://storage.googleapis.com/storage/v1/b?project=#{runtime.gcp_project_id}" => {
        "items" => [
          { "name" => organization_bundle.gcs_bucket_name },
          { "name" => orphan_bucket_name },
          { "name" => "manual-bucket" }
        ]
      },
      "https://storage.googleapis.com/storage/v1/b/#{organization_bundle.gcs_bucket_name}/o?prefix=node-bundles%2F" => {
        "items" => [
          { "name" => "node-bundles/a33333333333/desired_state.json" },
          { "name" => "node-bundles/a44444444444/desired_state.json" }
        ]
      },
      "https://storage.googleapis.com/storage/v1/b/#{orphan_bucket_name}/o" => {
        "items" => [
          { "name" => "node-bundles/a55555555555/desired_state.json" }
        ]
      }
    )
    iam = FakeIamService.new(
      pages: [
        AccountPage.new(
          accounts: [
            Account.new(email: organization_bundle.gar_writer_service_account_email),
            Account.new(email: environment_bundle.service_account_email),
            Account.new(email: "oba44444444444garpush@#{runtime.gcp_project_id}.iam.gserviceaccount.com"),
            Account.new(email: "eba44444444444@#{runtime.gcp_project_id}.iam.gserviceaccount.com"),
            Account.new(email: "manual@#{runtime.gcp_project_id}.iam.gserviceaccount.com")
          ],
          next_page_token: nil
        )
      ]
    )

    result = Maintenance::PruneZombieGcpResources.new(
      runtime_projects: [ runtime ],
      client: client,
      iam: iam,
      logger: logger
    ).call

    assert_equal 1, result.deleted_buckets
    assert_equal 2, result.deleted_bucket_objects
    assert_equal 1, result.deleted_repositories
    assert_equal 2, result.deleted_secrets
    assert_equal 2, result.deleted_service_accounts

    assert_equal [
      "https://secretmanager.googleapis.com/v1/projects/#{runtime.gcp_project_id}/secrets/eb-a44444444444-ingress-cloudflare-tunnel-token",
      "https://secretmanager.googleapis.com/v1/projects/#{runtime.gcp_project_id}/secrets/env-a55555555555-web-secret-key-base",
      "https://artifactregistry.googleapis.com/v1/projects/#{runtime.gcp_project_id}/locations/#{runtime.gar_region}/repositories/ob-a44444444444-apps",
      "https://storage.googleapis.com/storage/v1/b/#{organization_bundle.gcs_bucket_name}/o/node-bundles%2Fa44444444444%2Fdesired_state.json",
      "https://storage.googleapis.com/storage/v1/b/#{orphan_bucket_name}/o/node-bundles%2Fa55555555555%2Fdesired_state.json",
      "https://storage.googleapis.com/storage/v1/b/#{orphan_bucket_name}"
    ], client.deleted_uris

    assert_equal [
      "projects/#{runtime.gcp_project_id}/serviceAccounts/oba44444444444garpush@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      "projects/#{runtime.gcp_project_id}/serviceAccounts/eba44444444444@#{runtime.gcp_project_id}.iam.gserviceaccount.com"
    ], iam.deleted_accounts
  end

  test "refreshes live service accounts before pruning each phase" do
    runtime = RuntimeProject.default!
    logger = mock("logger")
    logger.stubs(:info)

    organization = Organization.create!(
      name: "org-#{SecureRandom.hex(3)}",
      runtime_project: runtime,
      gcp_project_id: runtime.gcp_project_id,
      gcp_project_number: runtime.gcp_project_number,
      workload_identity_pool: runtime.workload_identity_pool,
      workload_identity_provider: runtime.workload_identity_provider,
      gar_repository_region: runtime.gar_region,
      provisioning_status: Organization::PROVISIONING_READY
    )
    organization_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      token: "a11111111111",
      claimed_by_organization: organization,
      claimed_at: Time.current,
      gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-ob-a11111111111",
      gar_repository_name: "ob-a11111111111-apps",
      gar_repository_region: runtime.gar_region,
      gar_writer_service_account_email: "oba11111111111garpush@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
      status: OrganizationBundle::STATUS_CLAIMED
    )
    organization.update!(
      organization_bundle: organization_bundle,
      gcs_bucket_name: organization_bundle.gcs_bucket_name,
      gar_repository_name: organization_bundle.gar_repository_name,
      gar_repository_region: organization_bundle.gar_repository_region
    )

    created_bundle = nil
    client = Class.new(FakeClient) do
      def initialize(get_map, &on_secrets_list)
        super(get_map)
        @on_secrets_list = on_secrets_list
      end

      def get(uri)
        @on_secrets_list.call if uri.include?("/secrets") && @on_secrets_list
        super
      end
    end.new(
      {
        "https://secretmanager.googleapis.com/v1/projects/#{runtime.gcp_project_id}/secrets" => { "secrets" => [] },
        "https://artifactregistry.googleapis.com/v1/projects/#{runtime.gcp_project_id}/locations/#{runtime.gar_region}/repositories" => { "repositories" => [] },
        "https://storage.googleapis.com/storage/v1/b?project=#{runtime.gcp_project_id}" => { "items" => [] }
      }
    ) do
      created_bundle ||= EnvironmentBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        token: "a22222222222",
        service_account_email: "eba22222222222@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
        gcp_secret_name: "eb-a22222222222-ingress-cloudflare-tunnel-token",
        hostname: "a22222222222.devopsellence.test",
        cloudflare_tunnel_id: "tunnel-a222",
        status: EnvironmentBundle::STATUS_WARM,
        provisioned_at: Time.current
      )
    end
    iam = FakeIamService.new(
      pages: [
        AccountPage.new(
          accounts: [
            Account.new(email: created_bundle&.service_account_email || "eba22222222222@#{runtime.gcp_project_id}.iam.gserviceaccount.com")
          ],
          next_page_token: nil
        )
      ]
    )

    result = Maintenance::PruneZombieGcpResources.new(
      runtime_projects: [ runtime ],
      client: client,
      iam: iam,
      logger: logger
    ).call

    assert_equal 0, result.deleted_service_accounts
    assert_equal [], iam.deleted_accounts
    assert created_bundle
  end

  class FakeClient
    attr_reader :deleted_uris

    def initialize(get_map)
      @get_map = get_map
      @deleted_uris = []
    end

    def get(uri)
      Response.new(code: 200, body: JSON.generate(@get_map.fetch(uri, {})))
    end

    def delete(uri)
      @deleted_uris << uri
      Response.new(code: 200, body: "{}")
    end
  end

  class FakeIamService
    attr_reader :deleted_accounts

    def initialize(pages:)
      @pages_by_token = {}
      @pages_by_token[nil] = pages.first || AccountPage.new(accounts: [], next_page_token: nil)
      pages.each_cons(2) do |current, nxt|
        @pages_by_token[current.next_page_token] = nxt if current.next_page_token.present?
      end
      @deleted_accounts = []
    end

    def list_project_service_accounts(_project, page_token: nil)
      @pages_by_token.fetch(page_token) { AccountPage.new(accounts: [], next_page_token: nil) }
    end

    def delete_project_service_account(resource_name)
      @deleted_accounts << resource_name
    end
  end
end
