# frozen_string_literal: true

require "base64"
require "json"
require "test_helper"

module Gcp
  class EnvironmentSecretManagerTest < ActiveSupport::TestCase
    FakeResponse = Struct.new(:code, :body, keyword_init: true)

    class FakeClient
      attr_reader :versions, :deleted, :requests

      def initialize
        @policies = {}
        @versions = {}
        @deleted = []
        @requests = []
      end

      def get(uri)
        @requests << [ :get, uri ]
        case uri
        when /storage.googleapis.com\/storage\/v1\/b\/[^\/]+\/iam\z/
          FakeResponse.new(code: "200", body: JSON.generate({ bindings: [] }))
        when /artifactregistry.googleapis.com\/.*:getIamPolicy\z/
          FakeResponse.new(code: "200", body: JSON.generate({ bindings: [] }))
        when /:getIamPolicy\z/
          secret_name = uri.split("/secrets/").last.split(":").first
          FakeResponse.new(code: "200", body: JSON.generate(@policies[secret_name] || { bindings: [] }))
        else
          raise "unexpected uri: #{uri}"
        end
      end

      def post(uri, payload:)
        @requests << [ :post, uri ]
        case uri
        when /artifactregistry.googleapis.com\/.*:setIamPolicy\z/
          FakeResponse.new(code: "200", body: JSON.generate(payload))
        when /secretmanager.googleapis.com\/v1\/projects\/[^\/]+\/secrets\?secretId=/
          FakeResponse.new(code: "200", body: "{}")
        when /:addVersion\z/
          secret_name = uri.split("/secrets/").last.split(":").first
          @versions[secret_name] ||= []
          @versions[secret_name] << Base64.decode64(payload.fetch(:payload).fetch(:data))
          FakeResponse.new(code: "200", body: "{}")
        when /:setIamPolicy\z/
          secret_name = uri.split("/secrets/").last.split(":").first
          @policies[secret_name] = payload.fetch(:policy)
          FakeResponse.new(code: "200", body: JSON.generate(@policies[secret_name]))
        else
          raise "unexpected uri: #{uri}"
        end
      end

      def put(uri, payload:)
        @requests << [ :put, uri ]
        case uri
        when /storage.googleapis.com\/storage\/v1\/b\/[^\/]+\/iam\z/
          FakeResponse.new(code: "200", body: JSON.generate(payload))
        else
          raise "unexpected uri: #{uri}"
        end
      end

      def delete(uri)
        @requests << [ :delete, uri ]
        secret_name = uri.split("/secrets/").last
        @deleted << secret_name
        FakeResponse.new(code: "200", body: "{}")
      end
    end

    test "upserts a secret version and grants environment access" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "Production",
        gcp_project_id: "gcp-proj-a",
        gcp_project_number: "123456789",
        workload_identity_pool: "pool-a",
        workload_identity_provider: "provider-a"
      )
      ensure_test_environment_bundle!(environment)
      secret = environment.environment_secrets.new(service_name: "web", name: "SECRET_KEY_BASE")
      client = FakeClient.new
      iam = Class.new do
        def get_project_service_account(_name) = true
      end.new
      broker = Runtime::Broker::LocalClient.new(client: client, iam: iam)

      manager = EnvironmentSecretManager.new(broker: broker)
      manager.upsert!(environment_secret: secret, value: "super-secret")

      assert secret.persisted?
      assert_equal [ "super-secret" ], client.versions.fetch(secret.gcp_secret_name)
      assert_equal EnvironmentSecret.value_sha256("super-secret"), secret.reload.value_sha256
      policy = client.instance_variable_get(:@policies).fetch(secret.gcp_secret_name)
      bindings = policy[:bindings] || policy["bindings"] || []
      members = bindings.flat_map { |binding| binding[:members] || binding["members"] }
      assert_includes members, "serviceAccount:#{environment.service_account_email}"
    end

    test "does not add a new secret version when the value digest is unchanged" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "Production",
        gcp_project_id: "gcp-proj-a",
        gcp_project_number: "123456789",
        workload_identity_pool: "pool-a",
        workload_identity_provider: "provider-a"
      )
      ensure_test_environment_bundle!(environment)
      secret = environment.environment_secrets.create!(
        service_name: "web",
        name: "SECRET_KEY_BASE",
        value_sha256: EnvironmentSecret.value_sha256("super-secret")
      )
      client = FakeClient.new
      iam = Class.new do
        def get_project_service_account(_name) = true
      end.new
      broker = Runtime::Broker::LocalClient.new(client: client, iam: iam)

      manager = EnvironmentSecretManager.new(broker: broker)
      manager.upsert!(environment_secret: secret, value: "super-secret")

      assert_nil client.versions[secret.gcp_secret_name]
      assert_equal EnvironmentSecret.value_sha256("super-secret"), secret.reload.value_sha256
      policy = client.instance_variable_get(:@policies).fetch(secret.gcp_secret_name)
      bindings = policy[:bindings] || policy["bindings"] || []
      members = bindings.flat_map { |binding| binding[:members] || binding["members"] }
      assert_includes members, "serviceAccount:#{environment.service_account_email}"
    end

    test "skips runtime and iam reconciliation when recent secret access was already verified" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "Production",
        gcp_project_id: "gcp-proj-a",
        gcp_project_number: "123456789",
        workload_identity_pool: "pool-a",
        workload_identity_provider: "provider-a"
      )
      ensure_test_environment_bundle!(environment)
      secret = environment.environment_secrets.create!(
        service_name: "web",
        name: "SECRET_KEY_BASE",
        value_sha256: EnvironmentSecret.value_sha256("super-secret"),
        access_grantee_email: environment.service_account_email,
        access_verified_at: 1.hour.ago
      )
      client = FakeClient.new
      iam = Class.new do
        def get_project_service_account(_name) = true
      end.new
      broker = Runtime::Broker::LocalClient.new(client: client, iam: iam)

      manager = EnvironmentSecretManager.new(broker: broker)
      manager.upsert!(environment_secret: secret, value: "super-secret")

      assert_empty client.requests
      assert_nil client.versions[secret.gcp_secret_name]
      assert_equal environment.service_account_email, secret.reload.access_grantee_email
      assert secret.reload.access_verified_at.present?
    end

    test "deletes a secret from gcp and the database" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "Production",
        gcp_project_id: "gcp-proj-a",
        gcp_project_number: "123456789",
        workload_identity_pool: "pool-a",
        workload_identity_provider: "provider-a"
      )
      ensure_test_environment_bundle!(environment)
      secret = environment.environment_secrets.create!(service_name: "web", name: "SECRET_KEY_BASE")
      client = FakeClient.new
      iam = Class.new do
        def get_project_service_account(_name) = true
      end.new
      broker = Runtime::Broker::LocalClient.new(client: client, iam: iam)

      manager = EnvironmentSecretManager.new(broker: broker)
      manager.destroy!(environment_secret: secret)

      assert_equal [ secret.gcp_secret_name ], client.deleted
      assert_not EnvironmentSecret.exists?(secret.id)
    end
  end
end
