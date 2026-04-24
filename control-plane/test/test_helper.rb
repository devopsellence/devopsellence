require "openssl"
require_relative "../lib/devopsellence/test_env_defaults"

ENV["RAILS_ENV"] ||= "test"
Devopsellence::TestEnvDefaults::ENV.each do |key, value|
  ENV[key] = value
end
ENV.delete("DEVOPSELLENCE_IDP_SIGNING_KEY_VERSION")
ENV.delete("DEVOPSELLENCE_DESIRED_STATE_SIGNING_KEY_VERSION")
ENV.delete("DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_EMAIL")
ENV.delete("DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_PROJECT_ID")
ENV["DEVOPSELLENCE_IDP_PRIVATE_KEY_PEM"] ||= OpenSSL::PKey::RSA.generate(2048).to_pem
ENV["DEVOPSELLENCE_DESIRED_STATE_PRIVATE_KEY_PEM"] ||= OpenSSL::PKey::RSA.generate(2048).to_pem
require_relative "../config/environment"
require "rails/test_help"
require "json"
require "mocha/minitest"
require "securerandom"
require "thread"
require "webmock/minitest"

module ActiveSupport
  class TestCase
    # Run tests in parallel with specified workers
    parallelize(workers: ENV.fetch("PARALLEL_WORKERS", "1").to_i)

    # Setup all fixtures in test/fixtures/*.yml for all tests in alphabetical order.
    fixtures :all

    # Add more helper methods to be used by all tests here...
    ENV_MUTEX = Mutex.new

    def with_env(overrides)
      ENV_MUTEX.synchronize do
        original = {}
        original_runtime = Rails.application.config.x.devopsellence_runtime if Rails.application.config.x.respond_to?(:devopsellence_runtime)
        overrides.each do |key, value|
          original[key] = ENV[key]
          ENV[key] = value
        end
        Rails.application.config.x.devopsellence_runtime = Devopsellence::RuntimeConfig.load!(env: ENV.to_h)
        Trust::Keyring.reset! if defined?(Trust::Keyring)

        yield
      ensure
        overrides.each_key do |key|
          ENV[key] = original[key]
        end
        Rails.application.config.x.devopsellence_runtime = original_runtime if defined?(original_runtime)
        Trust::Keyring.reset! if defined?(Trust::Keyring)
      end
    end

    def without_http_basic(&block)
      with_env(
        "DEVOPSELLENCE_HTTP_BASIC_USERNAME" => nil,
        "DEVOPSELLENCE_HTTP_BASIC_PASSWORD" => nil,
        &block
      )
    end

    def with_runtime_config(overrides)
      original = Devopsellence::RuntimeConfig.current
      updated = original.dup
      overrides.each { |key, value| updated[key] = value }
      Devopsellence::RuntimeConfig.stubs(:current).returns(updated)
      yield
    end

    class FakeObjectStore
      attr_reader :writes

      def initialize
        @writes = []
      end

      def write_json!(object_path:, payload:, bucket: nil)
        stored_payload = ::JSON.parse(::JSON.generate(payload))
        parsed_payload = if stored_payload.is_a?(Hash) && stored_payload["payload_json"].present?
          ::JSON.parse(stored_payload["payload_json"])
        else
          stored_payload
        end
        entry = {
          bucket: bucket,
          object_path: object_path,
          payload: parsed_payload,
          envelope: stored_payload
        }
        @writes << entry
        "gs://#{bucket}/#{object_path}"
      end

      def write_json_batch!(entries:, bucket: nil)
        entries.map do |entry|
          write_json!(
            bucket: bucket,
            object_path: entry.fetch(:object_path),
            payload: entry.fetch(:payload)
          )
        end
      end

      def find_write(bucket:, object_path:)
        @writes.reverse.find { |entry| entry[:bucket] == bucket && entry[:object_path] == object_path }
      end

      def desired_state_payload(bucket:, object_path:)
        pointer = find_write(bucket:, object_path:)
        raise KeyError, "missing object #{bucket}/#{object_path}" unless pointer

        payload = pointer.fetch(:payload)
        return payload unless payload.is_a?(Hash) && payload["format"] == Nodes::DesiredStatePointer::FORMAT

        target = find_write(bucket:, object_path: payload.fetch("object_path"))
        raise KeyError, "missing pointed object #{bucket}/#{payload.fetch("object_path")}" unless target

        target.fetch(:payload)
      end
    end

    def with_object_store(store)
      Storage::ObjectStore.stubs(:build).returns(store)
      yield
    end

    def ensure_test_organization_runtime!(organization)
      runtime = Devopsellence::RuntimeConfig.current
      organization.update!(
        gcp_project_id: organization.gcp_project_id.presence || runtime.gcp_project_id,
        gcp_project_number: organization.gcp_project_number.presence || runtime.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool.presence || runtime.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider.presence || runtime.workload_identity_provider,
        gar_repository_region: organization.gar_repository_region.presence || runtime.gar_region,
        gcs_bucket_name: organization.gcs_bucket_name.presence || "#{runtime.gcs_bucket_prefix}-org-#{organization.id || "test"}",
        gar_repository_name: organization.gar_repository_name.presence || "org-#{organization.id || "test"}-apps",
        provisioning_status: Organization::PROVISIONING_READY,
        provisioning_error: nil
      )
    end

    def ensure_test_organization_bundle!(organization, runtime: RuntimeProject.default!, status: OrganizationBundle::STATUS_CLAIMED)
      ensure_test_organization_runtime!(organization)
      bundle = organization.organization_bundle || OrganizationBundle.create!(
        runtime_project: runtime,
        gcs_bucket_name: organization.gcs_bucket_name,
        gar_repository_name: organization.gar_repository_name,
        gar_repository_region: organization.gar_repository_region,
        gar_writer_service_account_email: "ob#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
        status: status,
        claimed_by_organization: (status == OrganizationBundle::STATUS_CLAIMED ? organization : nil)
      )
      organization.update!(runtime_project: runtime, organization_bundle: bundle)
      bundle
    end

    def ensure_test_environment_bundle!(environment, runtime: RuntimeProject.default!, status: EnvironmentBundle::STATUS_CLAIMED)
      organization = environment.project.organization
      organization_bundle = ensure_test_organization_bundle!(organization, runtime:)
      bundle = environment.environment_bundle || EnvironmentBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        claimed_by_environment: (status == EnvironmentBundle::STATUS_CLAIMED ? environment : nil),
        service_account_email: "eb#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
        gcp_secret_name: "eb-#{SecureRandom.hex(4)}-secret",
        hostname: "#{SecureRandom.alphanumeric(12).downcase}.devopsellence.test",
        cloudflare_tunnel_id: "tunnel-#{SecureRandom.hex(4)}",
        status: status,
        provisioned_at: Time.current
      )
      environment.update!(runtime_project: runtime, environment_bundle: bundle, service_account_email: bundle.service_account_email)
      bundle
    end

    def web_service_runtime(port: 3000, healthcheck_path: "/up", healthcheck_port: nil, command: nil, args: nil, env: {}, secret_refs: [], volumes: [], image: nil)
      {
        "kind" => "web",
        "image" => image,
        "command" => command,
        "args" => args,
        "env" => env,
        "secret_refs" => secret_refs,
        "ports" => [ { "name" => "http", "port" => port } ],
        "healthcheck" => { "path" => healthcheck_path, "port" => healthcheck_port || port },
        "volumes" => volumes
      }.compact
    end

    def worker_service_runtime(command: nil, args: nil, env: {}, secret_refs: [], volumes: [], image: nil)
      {
        "kind" => "worker",
        "image" => image,
        "command" => command,
        "args" => args,
        "env" => env,
        "secret_refs" => secret_refs,
        "volumes" => volumes
      }.compact
    end

    def release_runtime_json(services: nil, tasks: {}, ingress: :__default__)
      services ||= { "web" => web_service_runtime }
      ingress = { "service" => "web" } if ingress == :__default__
      ::JSON.generate(
        {
          "services" => services,
          "tasks" => tasks,
          "ingress" => ingress
        }.compact
      )
    end

    def issue_test_node!(organization: nil, name: nil, labels: [ Node::DEFAULT_LABEL ], managed: false, managed_provider: nil, managed_region: nil, managed_size_slug: nil, provider_server_id: nil, public_ip: nil)
      ensure_test_organization_runtime!(organization) if organization

      raw_access = SecureRandom.hex(Node::TOKEN_BYTES)
      raw_refresh = SecureRandom.hex(Node::TOKEN_BYTES)
      node = Node.create!(
        organization: organization,
        name: name,
        access_token_digest: Node.digest(raw_access),
        refresh_token_digest: Node.digest(raw_refresh),
        access_expires_at: Node::ACCESS_TTL.from_now,
        refresh_expires_at: Node::REFRESH_TTL.from_now,
        provisioning_status: Node::PROVISIONING_READY,
        provisioning_error: nil,
        managed: managed,
        managed_provider: managed_provider,
        managed_region: managed_region,
        managed_size_slug: managed_size_slug,
        provider_server_id: provider_server_id,
        public_ip: public_ip,
        labels_json: ::JSON.generate(labels),
        desired_state_bucket: organization&.gcs_bucket_name.to_s,
        desired_state_object_path: organization ? "nodes/#{SecureRandom.hex(6)}/desired_state.json" : ""
      )

      [node, raw_access, raw_refresh]
    end

    def with_successful_organization_runtime_provisioning
      runtime = RuntimeProject.default!
      OrganizationBundle.create!(
        runtime_project: runtime,
        gcs_bucket_name: "#{runtime.gcs_bucket_prefix}-org-#{SecureRandom.hex(3)}",
        gar_repository_name: "org-#{SecureRandom.hex(3)}-apps",
        gar_repository_region: runtime.gar_region,
        gar_writer_service_account_email: "ob#{SecureRandom.hex(4)}@#{runtime.gcp_project_id}.iam.gserviceaccount.com",
        status: OrganizationBundle::STATUS_WARM
      )
      yield
    end

    def with_agent_release_fetcher(fetcher)
      AgentReleases::Fetcher.stubs(:build).returns(fetcher)
      yield
    end

    def with_cli_release_fetcher(fetcher)
      CliReleases::Fetcher.stubs(:build).returns(fetcher)
      yield
    end

    def random_ingress_hostname
      "#{SecureRandom.alphanumeric(EnvironmentIngress::HOSTNAME_LENGTH).downcase}.devopsellence.io"
    end
  end
end
