# frozen_string_literal: true

require "securerandom"
require "test_helper"

module Cloudflare
  class EnvironmentIngressProvisionerTest < ActiveSupport::TestCase
    class FakeClient
      attr_reader :created_tunnels, :configured_tunnels, :dns_records, :deleted_dns_records, :token_requests

      def initialize
        @created_tunnels = []
        @configured_tunnels = []
        @dns_records = []
        @deleted_dns_records = []
        @token_requests = []
      end

      def create_tunnel(name:)
        @created_tunnels << name
        {
          "id" => "tunnel-#{created_tunnels.size}"
        }
      end

      def tunnel_token(tunnel_id:)
        @token_requests << tunnel_id
        "token-#{token_requests.size}"
      end

      def configure_tunnel(tunnel_id:, service:, hostname: nil, hostnames: nil)
        @configured_tunnels << {
          tunnel_id: tunnel_id,
          hostnames: Array(hostnames.presence || hostname),
          service: service
        }
      end

      def create_dns_cname(hostname:, target:)
        @dns_records << {
          hostname: hostname,
          target: target
        }
      end

      def delete_dns_records(hostname:, type: nil)
        @deleted_dns_records << {
          hostname: hostname,
          type: type
        }
      end
    end

    class FakeSecretManager
      attr_reader :tokens

      def initialize
        @tokens = []
      end

      def upsert_ingress_token!(environment_ingress:, value:)
        tokens << {
          ingress_id: environment_ingress.id,
          secret_name: environment_ingress.gcp_secret_name,
          value: value
        }
      end
    end

    test "provisions ingress, persists hostname, and stores token in gsm" do
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
      client = FakeClient.new
      secret_manager = FakeSecretManager.new
      slug = SecureRandom.alphanumeric(6).downcase

      ingress = EnvironmentIngressProvisioner.new(
        environment: environment,
        client: client,
        secret_manager: secret_manager,
        hostname_generator: -> { slug }
      ).call

      assert_equal environment.id, ingress.environment_id
      assert_equal "#{slug}.devopsellence.io", ingress.hostname
      assert_equal EnvironmentIngress::STATUS_READY, ingress.status
      assert_equal "https://#{slug}.devopsellence.io", ingress.public_url
      assert_equal "tunnel-1", ingress.cloudflare_tunnel_id
      assert_equal "env-#{environment.id}-ingress-cloudflare-tunnel-token", ingress.gcp_secret_name
      assert_equal [
        {
          ingress_id: ingress.id,
          secret_name: ingress.gcp_secret_name,
          value: "token-1"
        }
      ], secret_manager.tokens
      assert_equal [ "tunnel-1" ], client.token_requests
      assert_equal [ "env-#{environment.id}-#{slug}" ], client.created_tunnels
      assert_equal "http://devopsellence-envoy:8000", client.configured_tunnels.first[:service]
      assert_equal [ "#{slug}.devopsellence.io" ], client.configured_tunnels.first[:hostnames]
      assert_equal "tunnel-1.cfargotunnel.com", client.dns_records.first[:target]
    end

    test "reuses ready ingress without provisioning a second tunnel" do
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
      slug = SecureRandom.alphanumeric(6).downcase
      ingress = environment.create_environment_ingress!(
        hostname: "#{slug}.devopsellence.io",
        cloudflare_tunnel_id: "tunnel-1",
        gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
        status: EnvironmentIngress::STATUS_READY,
        provisioned_at: Time.current
      )

      result = EnvironmentIngressProvisioner.new(
        environment: environment,
        client: client = FakeClient.new,
        secret_manager: FakeSecretManager.new
      ).call

      assert_equal ingress.id, result.id
      assert_equal "#{slug}.devopsellence.io", result.hostname
      assert_equal [], client.created_tunnels
      assert_equal [
        {
          tunnel_id: "tunnel-1",
          hostnames: [ "#{slug}.devopsellence.io" ],
          service: "http://devopsellence-envoy:8000"
        }
      ], client.configured_tunnels
      assert_equal [
        { hostname: "#{slug}.devopsellence.io", type: "A" },
        { hostname: "#{slug}.devopsellence.io", type: "CNAME" }
      ], client.deleted_dns_records
      assert_equal [
        {
          hostname: "#{slug}.devopsellence.io",
          target: "tunnel-1.cfargotunnel.com"
        }
      ], client.dns_records
    end

    test "removes stale hosts supplied by the reconciler" do
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
      current_host = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"
      stale_host = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"
      environment.create_environment_ingress!(
        hostname: current_host,
        cloudflare_tunnel_id: "tunnel-1",
        gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
        status: EnvironmentIngress::STATUS_READY,
        provisioned_at: Time.current
      )
      client = FakeClient.new

      EnvironmentIngressProvisioner.new(
        environment: environment,
        client: client,
        secret_manager: FakeSecretManager.new,
        stale_hosts: [ stale_host ]
      ).call

      assert_equal [
        { hostname: stale_host, type: "A" },
        { hostname: stale_host, type: "CNAME" },
        { hostname: current_host, type: "A" },
        { hostname: current_host, type: "CNAME" }
      ], client.deleted_dns_records
    end

    test "normalizes stored mixed-case release hosts before updating routing" do
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
        image_digest: "sha256:#{"b" * 64}",
        runtime_json: JSON.generate(
          {
            "services" => { "web" => web_service_runtime },
            "tasks" => {},
            "ingress" => {
              "hosts" => ["App.Example.Test", "WWW.Example.Test"],
              "rules" => [
                {
                  "match" => { "host" => "App.Example.Test", "path_prefix" => "/" },
                  "target" => { "service" => "web", "port" => "http" }
                },
                {
                  "match" => { "host" => "WWW.Example.Test", "path_prefix" => "/" },
                  "target" => { "service" => "web", "port" => "http" }
                }
              ]
            }
          }
        )
      )
      environment.update!(current_release: release)
      environment.create_environment_ingress!(
        hostname: "app.example.test",
        cloudflare_tunnel_id: "tunnel-1",
        gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
        status: EnvironmentIngress::STATUS_READY,
        provisioned_at: Time.current
      )
      client = FakeClient.new

      ingress = EnvironmentIngressProvisioner.new(
        environment: environment,
        client: client,
        secret_manager: FakeSecretManager.new,
        release: release
      ).call

      assert_equal ["app.example.test", "www.example.test"], ingress.hosts
      assert_equal ["app.example.test", "www.example.test"], client.configured_tunnels.first[:hostnames]
    end

    test "merges bundled hostname with configured release hosts" do
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
      bundle = ensure_test_environment_bundle!(environment)
      release = project.releases.create!(
        git_sha: "a" * 40,
        revision: "rel-1",
        image_repository: "shop-app",
        image_digest: "sha256:#{"b" * 64}",
        runtime_json: JSON.generate(
          {
            "services" => { "web" => web_service_runtime },
            "tasks" => {},
            "ingress" => {
              "hosts" => ["app.example.test", "www.example.test"],
              "rules" => [
                {
                  "match" => { "host" => "app.example.test", "path_prefix" => "/" },
                  "target" => { "service" => "web", "port" => "http" }
                },
                {
                  "match" => { "host" => "www.example.test", "path_prefix" => "/" },
                  "target" => { "service" => "web", "port" => "http" }
                }
              ]
            }
          }
        )
      )
      environment.update!(current_release: release)
      environment.create_environment_ingress!(
        hostname: bundle.hostname,
        cloudflare_tunnel_id: bundle.cloudflare_tunnel_id,
        gcp_secret_name: bundle.gcp_secret_name,
        status: EnvironmentIngress::STATUS_READY,
        provisioned_at: bundle.provisioned_at
      )
      client = FakeClient.new

      ingress = EnvironmentIngressProvisioner.new(
        environment: environment,
        client: client,
        secret_manager: FakeSecretManager.new,
        release: release
      ).call

      assert_equal [bundle.hostname, "app.example.test", "www.example.test"], ingress.hosts
      assert_equal [bundle.hostname, "app.example.test", "www.example.test"], client.configured_tunnels.first[:hostnames]
    end

    test "restores tunnel routing from environment bundle data" do
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
      bundle = ensure_test_environment_bundle!(environment)
      environment.create_environment_ingress!(
        hostname: bundle.hostname,
        cloudflare_tunnel_id: bundle.cloudflare_tunnel_id,
        gcp_secret_name: bundle.gcp_secret_name,
        status: EnvironmentIngress::STATUS_DEGRADED,
        last_error: "no eligible public web nodes with fresh heartbeat and settled rollout",
        provisioned_at: bundle.provisioned_at
      )
      client = FakeClient.new

      result = EnvironmentIngressProvisioner.new(
        environment: environment,
        client: client,
        secret_manager: FakeSecretManager.new
      ).call

      assert_equal EnvironmentIngress::STATUS_READY, result.status
      assert_nil result.last_error
      assert_equal bundle.hostname, result.hostname
      assert_equal bundle.cloudflare_tunnel_id, result.cloudflare_tunnel_id
      assert_equal [], client.created_tunnels
      assert_equal [
        {
          tunnel_id: bundle.cloudflare_tunnel_id,
          hostnames: [ bundle.hostname ],
          service: "http://devopsellence-envoy:8000"
        }
      ], client.configured_tunnels
    end

    test "local ingress backend marks ingress ready without calling cloudflare" do
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
      client = FakeClient.new
      secret_manager = FakeSecretManager.new

      with_runtime_config(
        ingress_backend: "local",
        local_ingress_public_url: "http://127.0.0.1:18080",
        local_ingress_hostname_suffix: "local.devopsellence.test"
      ) do
        ingress = EnvironmentIngressProvisioner.new(
          environment: environment,
          client: client,
          secret_manager: secret_manager
        ).call

        assert_equal EnvironmentIngress::STATUS_READY, ingress.status
        assert_equal "http://127.0.0.1:18080", ingress.public_url
        assert_equal "local-env-#{environment.id}", ingress.cloudflare_tunnel_id
        assert_equal [], client.created_tunnels
        assert_equal [], secret_manager.tokens
      end
    end
  end
end
