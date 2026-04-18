#!/usr/bin/env ruby
# frozen_string_literal: true

require "digest"
require "fileutils"
require "json"
require "net/http"
require "open3"
require "openssl"
require "pathname"
require "securerandom"
require "shellwords"
require "socket"
require "time"
require "tmpdir"
require "timeout"
require "uri"
require "yaml"
require_relative "binary_artifacts"

class E2E
  include E2EBinaryArtifacts

  MONOREPO_ROOT = Pathname(__dir__).join("../..").expand_path
  ROOT = MONOREPO_ROOT.join("control-plane")

  INTERNAL_CONTROL_PLANE_PORT = 3000
  INTERNAL_GCP_MOCK_PORT = 4601
  INTERNAL_POSTGRES_PORT = 5432
  INTERNAL_REGISTRY_PORT = 5000
  APP_PORT = 9292
  APP_VOLUME_TARGET = "/data"
  RELEASE_MARKER_FILENAME = "release-marker.txt"
  DEFAULT_POSTGRES_IMAGE = "postgres:16"
  DEFAULT_REGISTRY_IMAGE = "registry:2"
  DEFAULT_RUNNER_IMAGE = "devopsellence/e2e-runner:local"
  DEFAULT_ENVOY_IMAGE = "docker.io/envoyproxy/envoy@sha256:d9b4a70739d92b3e28cd407f106b0e90d55df453d7d87773efd22b4429777fe8"
  DEFAULT_RUNTIME_BACKEND = "standalone"
  RUNNER_IMAGE_FINGERPRINT_LABEL = "devopsellence.e2e.runner_fingerprint"
  RELEASE_REGION = "local"
  CONTROL_PLANE_PROJECT = "devopsellence-e2e-control-plane"
  RUNTIME_PROJECT = "devopsellence-e2e-runtime"
  RUNTIME_PROJECT_NUMBER = "123456789012"
  WORKLOAD_IDENTITY_POOL = "projects/#{RUNTIME_PROJECT_NUMBER}/locations/global/workloadIdentityPools/devopsellence-e2e"
  WORKLOAD_IDENTITY_PROVIDER = "#{WORKLOAD_IDENTITY_POOL}/providers/devopsellence-e2e"
  CONTROL_PLANE_SA_PROJECT = CONTROL_PLANE_PROJECT
  CONTROL_PLANE_SA_ID = "devopsellence-control-plane"
  IDP_SIGNING_KEY = "projects/#{CONTROL_PLANE_PROJECT}/locations/global/keyRings/devopsellence-e2e/cryptoKeys/idp-signing/cryptoKeyVersions/1"
  DESIRED_STATE_SIGNING_KEY = "projects/#{CONTROL_PLANE_PROJECT}/locations/global/keyRings/devopsellence-e2e/cryptoKeys/desired-state-signing/cryptoKeyVersions/1"
  APP_PROBE_PATH = "/e2e"
  APP_HEALTH_PATH = "/up"
  PLAIN_ENV_NAME = "E2E_PLAIN_ENV"
  SECRET_VALUE_NAME = "E2E_SECRET_VALUE"
  SECRET_STDIN_NAME = "E2E_SECRET_STDIN"

  def initialize
    @run_id = ENV.fetch("DEVOPSELLENCE_E2E_RUN_ID", "").to_s.strip
    @run_id = "#{Time.now.utc.strftime('%Y%m%d%H%M%S')}-#{SecureRandom.hex(3)}" if @run_id.empty?
    @release_version = ENV.fetch("DEVOPSELLENCE_E2E_RELEASE_VERSION", "").to_s.strip
    @release_version = "v0.0.0-e2e.#{@run_id.tr('-', '.')}" if @release_version.empty?
    @checkout_root = resolve_checkout_root
    @workspace_root = resolve_workspace_root
    @cli_root = resolve_repo_root(%w[cli], "DEVOPSELLENCE_CLI_ROOT")
    @agent_root = resolve_repo_root(%w[agent], "DEVOPSELLENCE_AGENT_ROOT")
    @gcp_mock_root = resolve_repo_root(%w[test/support/gcp-mock], "DEVOPSELLENCE_GCP_MOCK_ROOT")
    @repo_mount_root = Pathname(ENV.fetch("DEVOPSELLENCE_E2E_REPO_MOUNT", ROOT.to_s)).expand_path
    @runner_image = ENV.fetch("DEVOPSELLENCE_E2E_RUNNER_IMAGE", DEFAULT_RUNNER_IMAGE)
    @postgres_image = ENV.fetch("DEVOPSELLENCE_E2E_POSTGRES_IMAGE", DEFAULT_POSTGRES_IMAGE)
    @registry_image = ENV.fetch("DEVOPSELLENCE_E2E_REGISTRY_IMAGE", DEFAULT_REGISTRY_IMAGE)
    @envoy_image = ENV.fetch("DEVOPSELLENCE_E2E_ENVOY_IMAGE", DEFAULT_ENVOY_IMAGE)
    @runtime_backend = ENV.fetch("DEVOPSELLENCE_E2E_RUNTIME_BACKEND", DEFAULT_RUNTIME_BACKEND).to_s.strip
    @auto_build_runner_image = ENV.fetch("DEVOPSELLENCE_E2E_BUILD_RUNNER_IMAGE", @runner_image == DEFAULT_RUNNER_IMAGE ? "1" : "0") == "1"
    @pull_helper_images = ENV.fetch("DEVOPSELLENCE_E2E_PULL_HELPER_IMAGES", "1") == "1"
    @state_dir = ROOT.join("tmp/e2e", @run_id)
    @state_mount_dir = @repo_mount_root.join("tmp/e2e", @run_id)
    @app_root_dir = Pathname(Dir.tmpdir).join("devopsellence-e2e", @run_id)
    @app_dir = @app_root_dir.join("app")
    @rails_tmp_dir = @state_dir.join("control-plane/tmp")
    @rails_log_dir = @state_dir.join("control-plane/log")
    @agent_state_dir = @state_dir.join("agent")
    @image_build_dir = @state_dir.join("images")
    @log_dir = ROOT.join("log")
    @run_labels = {
      "devopsellence.e2e" => "1",
      "devopsellence.e2e.run_id" => @run_id
    }
    @network = "devopsellence-e2e-#{@run_id}"
    @postgres_container = "devopsellence-e2e-postgres-#{@run_id}"
    @registry_container = "devopsellence-e2e-registry-#{@run_id}"
    @gcp_mock_container = "devopsellence-e2e-gcp-mock-#{@run_id}"
    @web_container = "devopsellence-e2e-web-#{@run_id}"
    @jobs_container = "devopsellence-e2e-jobs-#{@run_id}"
    @agent_container = "devopsellence-e2e-agent-#{@run_id}"
    @agent_envoy_container = "devopsellence-e2e-envoy-#{@run_id}"
    @agent_image = "devopsellence/e2e-agent:#{@run_id}"
    @gcp_mock_image = "devopsellence/e2e-gcp-mock:#{@run_id}"
    @control_plane_port = Integer(ENV.fetch("DEVOPSELLENCE_E2E_CONTROL_PLANE_PORT", available_port(13_300).to_s))
    @gcp_mock_port = Integer(ENV.fetch("DEVOPSELLENCE_E2E_GCP_MOCK_PORT", available_port(14_601).to_s))
    @registry_port = Integer(ENV.fetch("DEVOPSELLENCE_E2E_REGISTRY_PORT", available_port(14_602).to_s))
    @ingress_port = Integer(ENV.fetch("DEVOPSELLENCE_E2E_INGRESS_PORT", available_port(18_080).to_s))
    @database_name = "devopsellence_e2e_#{@run_id.tr('-', '_')}"
    @host_control_plane_base_url = ENV.fetch("DEVOPSELLENCE_E2E_BASE_URL", "http://127.0.0.1:#{@control_plane_port}")
    @host_gcp_mock_base_url = "http://127.0.0.1:#{@gcp_mock_port}"
    @host_registry = "127.0.0.1:#{@registry_port}"
    @local_ingress_url = ENV.fetch("DEVOPSELLENCE_LOCAL_INGRESS_PUBLIC_URL", "http://127.0.0.1:#{@ingress_port}")
    @internal_control_plane_base_url = "http://web:#{INTERNAL_CONTROL_PLANE_PORT}"
    @internal_gcp_mock_base_url = "http://gcp-mock:#{INTERNAL_GCP_MOCK_PORT}"
    @internal_database_url = "postgres://postgres:postgres@postgres:#{INTERNAL_POSTGRES_PORT}/#{@database_name}"
    @user_token = nil
    @project_name = "e2e-#{SecureRandom.hex(3)}"
    @environment_name = "e2e"
    @registry_username = "e2e-user"
    @registry_password = "e2e-password-#{SecureRandom.hex(6)}"
    @idp_private_key_pem = OpenSSL::PKey::RSA.generate(2048).to_pem
    @desired_state_private_key_pem = OpenSSL::PKey::RSA.generate(2048).to_pem
    @keep_runtime = ENV["DEVOPSELLENCE_E2E_KEEP"] == "1"
    @container_log_paths = {
      @postgres_container => @log_dir.join("e2e-postgres-#{@run_id}.log"),
      @registry_container => @log_dir.join("e2e-registry-#{@run_id}.log"),
      @gcp_mock_container => @log_dir.join("e2e-gcp-mock-#{@run_id}.log"),
      @web_container => @log_dir.join("e2e-web-#{@run_id}.log"),
      @jobs_container => @log_dir.join("e2e-jobs-#{@run_id}.log"),
      @agent_container => @log_dir.join("e2e-agent-#{@run_id}.log"),
      @agent_envoy_container => @log_dir.join("e2e-envoy-#{@run_id}.log")
    }
    raise "unsupported DEVOPSELLENCE_E2E_RUNTIME_BACKEND=#{@runtime_backend.inspect}" unless %w[gcp standalone].include?(@runtime_backend)
  end

  def call
    prepare_directories!

    step("prerequisites") { ensure_local_prerequisites! }
    step("prepare local binary artifacts") { prepare_binary_artifacts! }
    step("build gcp-mock image") { build_gcp_mock_image! }
    step("build warm agent image") { build_warm_agent_image! }
    step("network") { create_network! }
    step("postgres") { start_postgres! }
    step("registry") { start_registry! }
    step("gcp-mock") { start_gcp_mock! }
    step("control plane") { start_control_plane! }
    step("download endpoints") { assert_release_downloads! }
    step("auth token") { create_user_token! }
    step("warm bundles") { warm_runtime_bundles! }
    step("warm agent") { start_warm_agent! }
    step("deploy") { run_deploy_flow! }
    step("assertions") { assert_runtime_state! }

    puts "\n[ok] e2e passed"
  ensure
    teardown!
  end

  private

    def step(name)
      puts "\n==> #{name}"
      yield
    end

    def prepare_directories!
      FileUtils.mkdir_p(@log_dir)
      FileUtils.rm_rf(@state_dir)
      FileUtils.rm_rf(@app_root_dir)
      FileUtils.rm_rf(@agent_state_dir)
      FileUtils.mkdir_p(@app_dir)
      FileUtils.mkdir_p(@rails_tmp_dir.join("pids"))
      FileUtils.mkdir_p(@rails_log_dir)
      FileUtils.mkdir_p(@agent_state_dir)
      FileUtils.mkdir_p(@image_build_dir)
    end

    def ensure_local_prerequisites!
      raise "repo mount root not found: #{@repo_mount_root}" unless @repo_mount_root.exist?

      ensure_runner_image!
      ensure_helper_image!(@postgres_image)
      ensure_helper_image!(@registry_image)
      ensure_helper_image!(@envoy_image)
    end

    def ensure_runner_image!
      return if runner_image_current?

      if @auto_build_runner_image
        build_runner_image!
        return
      end

      if docker_image_present?(@runner_image)
        raise "runner image #{@runner_image} is stale for this checkout; rebuild it or set DEVOPSELLENCE_E2E_BUILD_RUNNER_IMAGE=1"
      end

      raise "runner image #{@runner_image} missing locally; build it or set DEVOPSELLENCE_E2E_BUILD_RUNNER_IMAGE=1"
    end

    def ensure_helper_image!(image)
      return if docker_image_present?(image)

      if @pull_helper_images
        puts "[pull] helper image #{image}"
        run!("docker", "pull", image, chdir: ROOT.to_s, timeout: 1800)
        return
      end

      raise "helper image missing locally: #{image}; pull it or set DEVOPSELLENCE_E2E_PULL_HELPER_IMAGES=1"
    end

    def build_runner_image!
      puts "[build] runner image #{@runner_image}"
      run!(
        "docker", "build",
        "--label", "#{RUNNER_IMAGE_FINGERPRINT_LABEL}=#{runner_image_fingerprint}",
        "-f", runner_dockerfile.to_s,
        "-t", @runner_image,
        ROOT.to_s,
        chdir: MONOREPO_ROOT.to_s,
        timeout: 3600
      )
    end

    def runner_image_current?
      return false unless docker_image_present?(@runner_image)

      docker_image_label(@runner_image, RUNNER_IMAGE_FINGERPRINT_LABEL) == runner_image_fingerprint
    end

    def runner_image_fingerprint
      @runner_image_fingerprint ||= begin
        digest = Digest::SHA256.new
        runner_build_context_paths.each do |path|
          digest << path.relative_path_from(MONOREPO_ROOT).to_s
          digest << "\0"
          digest << Digest::SHA256.file(path).hexdigest
          digest << "\0"
        end
        digest.hexdigest
      end
    end

    def runner_build_context_paths
      paths = [
        MONOREPO_ROOT.join("test/e2e/runner.Dockerfile"),
        ROOT.join("Gemfile"),
        ROOT.join("Gemfile.lock")
      ]
      vendor_root = ROOT.join("vendor")
      if vendor_root.exist?
        paths.concat(vendor_root.glob("**/*").select(&:file?).sort)
      end
      paths
    end

    def runner_dockerfile
      MONOREPO_ROOT.join("test/e2e/runner.Dockerfile")
    end

    def build_gcp_mock_image!
      binary_path = @image_build_dir.join("gcp-mock/gcp-mock")
      FileUtils.mkdir_p(binary_path.dirname)
      run!(
        go_binary, "build",
        "-trimpath",
        "-o", binary_path.to_s,
        "./cmd/gcp-mock",
        chdir: @gcp_mock_root.to_s,
        timeout: 600,
        env: {
          "CGO_ENABLED" => "0",
          "GOCACHE" => @gcp_mock_root.join(".gocache").to_s
        }
      )
      dockerfile = binary_path.dirname.join("Dockerfile")
      dockerfile.write(<<~DOCKERFILE)
        FROM scratch
        COPY gcp-mock /usr/local/bin/gcp-mock
        ENTRYPOINT ["/usr/local/bin/gcp-mock"]
      DOCKERFILE
      run!("docker", "build", "-t", @gcp_mock_image, binary_path.dirname.to_s, chdir: ROOT.to_s, timeout: 600)
    end

    def build_warm_agent_image!
      image_dir = @image_build_dir.join("agent")
      FileUtils.rm_rf(image_dir)
      FileUtils.mkdir_p(image_dir)
      FileUtils.cp(agent_binary, image_dir.join("devopsellence"))
      image_dir.join("Dockerfile").write(<<~DOCKERFILE)
        FROM scratch
        COPY devopsellence /usr/local/bin/devopsellence
        ENTRYPOINT ["/usr/local/bin/devopsellence"]
      DOCKERFILE
      run!("docker", "build", "-t", @agent_image, image_dir.to_s, chdir: ROOT.to_s, timeout: 600)
    end

    def create_network!
      run!("docker", "network", "create", *docker_label_args, @network, chdir: ROOT.to_s, timeout: 60)
    end

    def start_postgres!
      run!(
        "docker", "run", "-d", "--rm",
        "--name", @postgres_container,
        "--network", @network,
        "--network-alias", "postgres",
        *docker_label_args,
        "-e", "POSTGRES_USER=postgres",
        "-e", "POSTGRES_PASSWORD=postgres",
        "-e", "POSTGRES_DB=postgres",
        @postgres_image,
        chdir: ROOT.to_s,
        timeout: 120
      )

      wait_until!(timeout: 60) do
        system_success?("docker", "exec", @postgres_container, "pg_isready", "-U", "postgres", chdir: ROOT.to_s)
      end
    end

    def start_registry!
      run!(
        "docker", "run", "-d", "--rm",
        "--name", @registry_container,
        "--network", @network,
        "--network-alias", "registry",
        *docker_label_args,
        "-p", "127.0.0.1:#{@registry_port}:#{INTERNAL_REGISTRY_PORT}",
        @registry_image,
        chdir: ROOT.to_s,
        timeout: 120
      )
    end

    def start_gcp_mock!
      run!(
        "docker", "run", "-d", "--rm",
        "--name", @gcp_mock_container,
        "--network", @network,
        "--network-alias", "gcp-mock",
        *docker_label_args,
        "-p", "127.0.0.1:#{@gcp_mock_port}:#{INTERNAL_GCP_MOCK_PORT}",
        @gcp_mock_image,
        "--listen", "0.0.0.0:#{INTERNAL_GCP_MOCK_PORT}",
        chdir: ROOT.to_s,
        timeout: 120
      )
      wait_http_ok!("#{@host_gcp_mock_base_url}/__admin/state", timeout: 30)
      http_post_json("#{@host_gcp_mock_base_url}/__admin/reset", {})
    end

    def start_control_plane!
      run_runner!("bin/rails db:prepare", timeout: 300)
      start_runner_service(
        name: @web_container,
        network_alias: "web",
        host_port: @control_plane_port,
        command: "rm -f tmp/pids/server.pid && bin/rails server -b 0.0.0.0 -p #{INTERNAL_CONTROL_PLANE_PORT}"
      )
      start_runner_service(
        name: @jobs_container,
        network_alias: "jobs",
        command: "bin/jobs"
      )
      wait_http_ok!("#{@host_control_plane_base_url}/up", timeout: 120)
    end

    def start_runner_service(name:, network_alias:, command:, host_port: nil)
      args = [
        "docker", "run", "-d", "--rm",
        "--name", name,
        "--network", @network,
        "--network-alias", network_alias,
        *docker_label_args,
        *runner_mount_args,
        *runner_env_args
      ]
      if host_port
        args += [ "-p", "127.0.0.1:#{host_port}:#{INTERNAL_CONTROL_PLANE_PORT}" ]
      end
      args += [ @runner_image, "bash", "-lc", command ]
      run!(*args, chdir: ROOT.to_s, timeout: 120)
    end

    def assert_release_downloads!
      assert_artifact_redirect!(
        "/cli/download?version=#{@release_version}&os=linux&arch=amd64",
        "https://github.com/devopsellence/devopsellence/releases/download/cli-#{@release_version}/linux-amd64"
      )
      assert_artifact_redirect!(
        "/agent/download?version=#{@release_version}&os=linux&arch=amd64",
        "https://github.com/devopsellence/devopsellence/releases/download/agent-#{@release_version}/linux-amd64"
      )
      assert_artifact_redirect!(
        "/cli/checksums?version=#{@release_version}",
        "https://github.com/devopsellence/devopsellence/releases/download/cli-#{@release_version}/SHA256SUMS"
      )
      assert_artifact_redirect!(
        "/agent/checksums?version=#{@release_version}",
        "https://github.com/devopsellence/devopsellence/releases/download/agent-#{@release_version}/SHA256SUMS"
      )
    end

    def create_user_token!
      payload = rails_json!(<<~RUBY)
        user = User.create!(email: "e2e-#{@run_id}@example.com", confirmed_at: Time.current)
        _token, raw = ApiToken.issue_ci_token!(user: user, name: "e2e-#{@run_id}")
        puts({ access_token: raw }.to_json)
      RUBY
      @user_token = payload.fetch("access_token")
    end

    def warm_runtime_bundles!
      rails_eval!("Runtime::BundlesReconciler.new.call")
      summary = rails_json!(<<~RUBY)
        runtime = RuntimeProject.default!
        puts({
          organization_bundles: OrganizationBundle.where(runtime_project: runtime, status: OrganizationBundle::STATUS_WARM).count,
          environment_bundles: EnvironmentBundle.where(runtime_project: runtime, status: EnvironmentBundle::STATUS_WARM).count,
          node_bundles: NodeBundle.where(runtime_project: runtime, status: NodeBundle::STATUS_WARM).count
        }.to_json)
      RUBY
      raise "organization bundle warm pool empty" if summary.fetch("organization_bundles") < 1
      raise "environment bundle warm pool empty" if summary.fetch("environment_bundles") < 1
      raise "node bundle warm pool empty" if summary.fetch("node_bundles") < 1
    end

    def start_warm_agent!
      payload = rails_json!(<<~RUBY)
        record, raw = NodeBootstrapToken.issue!(
          purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
          managed_provider: "local",
          managed_region: "local",
          managed_size_slug: "docker"
        )
        record.update!(provider_server_id: "local-#{@run_id}", public_ip: "127.0.0.1")
        puts({ bootstrap_id: record.id, bootstrap_token: raw }.to_json)
      RUBY

      FileUtils.mkdir_p(@agent_state_dir)
      agent_host_dir = @agent_state_dir
      auth_state_path = agent_host_dir.join("auth.json")
      cloud_init_path = agent_host_dir.join("instance-data.json")
      envoy_bootstrap_path = agent_host_dir.join("envoy/envoy.yaml")
      File.write(cloud_init_path, JSON.generate({ v1: { instance_id: "local-#{@run_id}" } }))

      run!(
        "docker", "run", "-d", "--rm",
        "--name", @agent_container,
        "--network", @network,
        *docker_label_args,
        "-v", "/var/run/docker.sock:/var/run/docker.sock",
        "-v", "#{agent_host_dir}:#{agent_host_dir}",
        @agent_image,
        "--control-plane-base-url", @internal_control_plane_base_url,
        "--bootstrap-token", payload.fetch("bootstrap_token"),
        "--node-name", "warm-#{@run_id}",
        "--cloud-init-instance-data-path", cloud_init_path.to_s,
        "--auth-state-path", auth_state_path.to_s,
        "--network", @network,
        "--envoy-container", @agent_envoy_container,
        "--envoy-bootstrap-path", envoy_bootstrap_path.to_s,
        "--envoy-port", @ingress_port.to_s,
        "--envoy-image", @envoy_image,
        "--prefetch-system-images=false",
        *agent_gcp_args,
        chdir: ROOT.to_s,
        timeout: 120
      )

      bootstrap_id = payload.fetch("bootstrap_id")
      wait_until!(timeout: 300) do
        unless docker_container_running?(@agent_container)
          raise "warm agent exited before registration\n#{docker_logs_excerpt(@agent_container, lines: 40)}"
        end

        readiness = rails_json!(<<~RUBY)
          bootstrap = NodeBootstrapToken.find(#{bootstrap_id})
          node = bootstrap.node
          puts({
            ready: node.present? && node.provisioning_status == Node::PROVISIONING_READY,
            node_id: node&.id
          }.to_json)
        RUBY
        readiness.fetch("ready")
      end
    end

    def run_deploy_flow!
      secret_value = "value-#{@run_id}"
      stdin_secret_value = "stdin-#{@run_id}"
      plain_env_value = "env-#{@run_id}"
      release_marker_value = "release-#{@run_id}"

      scaffold_app!(plain_env_value, release_marker_value:)
      configure_registry_for_standalone! if standalone_runtime?

      run!(
        cli_binary.to_s, "secret", "set",
        "--service", "web",
        SECRET_VALUE_NAME,
        "--value", secret_value,
        chdir: @app_dir.to_s,
        timeout: 180,
        env: cli_env
      )
      run!(
        cli_binary.to_s, "secret", "set",
        "--service", "web",
        SECRET_STDIN_NAME,
        "--stdin",
        chdir: @app_dir.to_s,
        timeout: 180,
        env: cli_env,
        input: stdin_secret_value
      )

      deploy_output = run!(
        cli_binary.to_s, "deploy", "--non-interactive",
        chdir: @app_dir.to_s,
        timeout: 1800,
        env: cli_env
      )
      raise "deploy did not settle" unless deploy_succeeded?(deploy_output)
      raise "deploy did not report local ingress URL" unless deploy_output.include?(@local_ingress_url)

      desired_state = rails_json!(<<~RUBY)
        environment = Environment.joins(:project).find_by!(projects: { name: #{@project_name.inspect} }, name: #{@environment_name.inspect})
        node = environment.nodes.order(:id).first
        document = StandaloneDesiredStateDocument.find_by!(node: node, sequence: node.desired_state_sequence)
        envelope = JSON.parse(document.payload_json)
        desired = JSON.parse(envelope.fetch("payload_json"))
        release_task = desired.fetch("environments").flat_map { |environment| environment.fetch("tasks", []) }.find { |task| task["name"] == "release" } || {}
        puts({
          revision: desired["revision"],
          release_task_env: release_task["env"],
          release_task_secret_refs: release_task["secretRefs"],
          release_task_command: release_task["command"]
        }.to_json)
      RUBY
      raise "desired state missing revision" if desired_state.fetch("revision").to_s.empty?
      release_task_env = desired_state.fetch("release_task_env")
      secret_refs = desired_state.fetch("release_task_secret_refs")
      command = desired_state.fetch("release_task_command")
      raise "release task env missing from desired state" unless release_task_env.is_a?(Hash)
      raise "plain env missing from desired state" unless release_task_env.fetch(PLAIN_ENV_NAME) == plain_env_value
      raise "secret value ref missing from desired state" unless secret_refs.is_a?(Hash) && secret_refs.key?(SECRET_VALUE_NAME)
      raise "stdin secret ref missing from desired state" unless secret_refs.is_a?(Hash) && secret_refs.key?(SECRET_STDIN_NAME)
      raise "release marker missing from release task" unless Array(command).include?(release_marker_value)

      return if standalone_runtime?

      current_image = rails_json!(<<~RUBY).fetch("image_reference")
        environment = Environment.joins(:project).find_by!(projects: { name: #{@project_name.inspect} }, name: #{@environment_name.inspect})
        release = environment.current_release
        puts({ image_reference: release.image_reference_for(environment.project.organization) }.to_json)
      RUBY
      image_deploy_output = run!(
        cli_binary.to_s, "deploy", "--non-interactive", "--image", current_image,
        chdir: @app_dir.to_s,
        timeout: 900,
        env: cli_env
      )
      raise "image deploy did not settle" unless deploy_succeeded?(image_deploy_output)
    end

    def assert_runtime_state!
      environment = rails_json!(<<~RUBY)
        environment = Environment.joins(:project).find_by!(projects: { name: #{@project_name.inspect} }, name: #{@environment_name.inspect})
        node = environment.nodes.order(:id).first
        puts({
          environment_id: environment.id,
          desired_state_bucket: node&.desired_state_bucket,
          desired_state_object_path: node&.desired_state_object_path,
          secret_refs: environment.environment_secrets.order(:name).map(&:secret_ref),
          standalone_desired_state_documents: StandaloneDesiredStateDocument.where(node: node).count,
          bundle_statuses: {
            organization: environment.project.organization.organization_bundle&.status,
            environment: environment.environment_bundle&.status,
            node: node&.node_bundle&.status
          }
        }.to_json)
      RUBY

      state = http_json("#{@host_gcp_mock_base_url}/__admin/state")
      events = http_json("#{@host_gcp_mock_base_url}/__admin/events").fetch("events")

      raise "organization bundle not claimed" unless environment.fetch("bundle_statuses").fetch("organization") == "claimed"
      raise "environment bundle not claimed" unless environment.fetch("bundle_statuses").fetch("environment") == "claimed"
      raise "node bundle not claimed" unless environment.fetch("bundle_statuses").fetch("node") == "claimed"

      if standalone_runtime?
        raise "standalone desired state bucket should be blank" unless environment.fetch("desired_state_bucket").to_s.empty?
        raise "standalone desired state object path missing" if environment.fetch("desired_state_object_path").to_s.empty?
        raise "standalone desired state document missing" unless environment.fetch("standalone_desired_state_documents") >= 1
        environment.fetch("secret_refs").each do |secret_ref|
          raise "expected standalone secret ref, got #{secret_ref.inspect}" unless secret_ref.start_with?("#{@internal_control_plane_base_url}/api/v1/agent/secrets/")
        end
        raise "standalone runtime unexpectedly wrote desired state to gcp-mock gcs" if events.any? { |entry| entry["type"] == "object_written" }
        raise "standalone runtime unexpectedly accessed gcp-mock secret manager" if events.any? { |entry| entry["type"] == "secret_accessed" }
        raise "standalone runtime unexpectedly minted gcp-mock google access tokens" if events.any? { |entry| entry["type"] == "access_token_generated" }
      else
        bucket_name = environment.fetch("desired_state_bucket")
        object_name = environment.fetch("desired_state_object_path")
        bucket = state.fetch("buckets").fetch(bucket_name)
        raise "desired state object missing from gcp-mock gcs" unless bucket.fetch("objects").key?(object_name)

        environment.fetch("secret_refs").each do |secret_ref|
          uri = URI.parse(secret_ref)
          parts = [ uri.host, *uri.path.to_s.split("/").reject(&:empty?) ]
          secret_name = parts[3]
          state.fetch("secrets").fetch("#{RUNTIME_PROJECT}/#{secret_name}")
        end

        raise "expected gcp-mock secret access event" unless events.any? { |entry| entry["type"] == "secret_accessed" }
        raise "expected gcp-mock desired state write" unless events.any? { |entry| entry["type"] == "object_written" }
        http_post_json("#{@host_gcp_mock_base_url}/__admin/assert", { event_type: "access_token_generated", min_count: 1 })
      end

      assert_direct_dns_feature!
    end

    def assert_direct_dns_feature!
      deployed_environment = rails_json!(<<~RUBY)
        environment = Environment.joins(:project).find_by!(projects: { name: #{@project_name.inspect} }, name: #{@environment_name.inspect})
        web_nodes = environment.nodes.select { |node| node.labeled?("web") }
        puts({
          web_nodes: web_nodes.map { |node| { name: node.name, capabilities: node.capabilities } },
          missing_capability_names: environment.assigned_web_nodes_missing_direct_dns_capability.map(&:name)
        }.to_json)
      RUBY

      web_nodes = deployed_environment.fetch("web_nodes")
      raise "expected assigned web node after deploy" if web_nodes.empty?
      unless web_nodes.any? { |node| Array(node.fetch("capabilities")).include?("direct_dns_ingress.v1") }
        if standalone_runtime?
          puts "skipping direct_dns e2e assertion: assigned standalone web node did not advertise direct_dns_ingress.v1"
          return
        end

        raise "expected warm agent direct_dns capability"
      end
      raise "assigned web nodes missing direct_dns capability: #{deployed_environment.fetch('missing_capability_names').join(', ')}" if deployed_environment.fetch("missing_capability_names").any?

      direct_dns_environment_name = "direct-dns"
      create_output = cli_json!("context", "env", "create", direct_dns_environment_name, "--ingress-strategy", "direct_dns", timeout: 180)
      raise "direct_dns environment create did not report requested ingress strategy" unless create_output.dig("environment", "ingress_strategy") == "direct_dns"

      status_output = cli_json!("status", "--env", direct_dns_environment_name, timeout: 180)
      raise "status did not report direct_dns ingress strategy" unless status_output.dig("environment", "ingress_strategy") == "direct_dns"

      persisted_environment = rails_json!(<<~RUBY)
        environment = Environment.joins(:project).find_by!(projects: { name: #{@project_name.inspect} }, name: #{direct_dns_environment_name.inspect})
        puts({ ingress_strategy: environment.ingress_strategy }.to_json)
      RUBY
      raise "environment ingress strategy was not persisted" unless persisted_environment.fetch("ingress_strategy") == "direct_dns"
    end

    def scaffold_app!(plain_env_value, release_marker_value:)
      FileUtils.mkdir_p(@app_dir)
      init_git_repo!
      set_workspace_mode!
      run!(
        cli_binary.to_s, "setup",
        "--json",
        "--non-interactive",
        "--project", @project_name,
        "--env", @environment_name,
        chdir: @app_dir.to_s,
        timeout: 180,
        env: cli_env
      )
      write_app_files!
      build_app_server_binary!
      update_app_config!(plain_env_value, release_marker_value:)
      commit_all!("Initialize e2e app")
    end

    def set_workspace_mode!
      output = run!(
        cli_binary.to_s, "mode", "use", "shared",
        chdir: @app_dir.to_s,
        timeout: 30,
        env: cli_env
      )
      raise "mode use shared did not confirm shared mode" unless output.include?("Mode: shared")
    end

    def write_app_files!
      @app_dir.join("Dockerfile").write(<<~DOCKERFILE)
        FROM scratch
        COPY server /server
        EXPOSE #{APP_PORT}
        ENTRYPOINT ["/server"]
      DOCKERFILE

      @app_dir.join("server.go").write(<<~GO)
        package main

        import (
          "encoding/json"
          "fmt"
          "log"
          "net/http"
          "os"
        )

        func main() {
          if len(os.Args) > 1 {
            switch os.Args[1] {
            case "release-task":
              runTask("release", os.Args[2:])
              return
            }
          }

          releaseMarker, err := readOptionalFile("#{APP_VOLUME_TARGET}/#{RELEASE_MARKER_FILENAME}")
          if err != nil {
            log.Fatalf("read release marker: %v", err)
          }

          mux := http.NewServeMux()
          mux.HandleFunc("#{APP_HEALTH_PATH}", func(w http.ResponseWriter, r *http.Request) {
            _, _ = w.Write([]byte("ok"))
          })
          mux.HandleFunc("#{APP_PROBE_PATH}", func(w http.ResponseWriter, r *http.Request) {
            payload := map[string]string{
              "plain_env":      os.Getenv("#{PLAIN_ENV_NAME}"),
              "secret_value":   os.Getenv("#{SECRET_VALUE_NAME}"),
              "stdin_secret":   os.Getenv("#{SECRET_STDIN_NAME}"),
              "release_marker": releaseMarker,
            }
            w.Header().Set("Content-Type", "application/json")
            if err := json.NewEncoder(w).Encode(payload); err != nil {
              http.Error(w, err.Error(), http.StatusInternalServerError)
            }
          })

          if err := http.ListenAndServe(":#{APP_PORT}", mux); err != nil {
            log.Fatal(err)
          }
        }

        func runTask(name string, args []string) {
          if len(args) != 2 {
            log.Fatalf("%s task requires path and value", name)
          }

          if err := os.MkdirAll("#{APP_VOLUME_TARGET}", 0o755); err != nil {
            log.Fatalf("mkdir data dir: %v", err)
          }
          if err := os.WriteFile(args[0], []byte(args[1]), 0o644); err != nil {
            log.Fatalf("write %s marker: %v", name, err)
          }
          _, _ = fmt.Fprintf(os.Stdout, "%s marker written to %s\\n", name, args[0])
        }

        func readOptionalFile(path string) (string, error) {
          data, err := os.ReadFile(path)
          if err == nil {
            return string(data), nil
          }
          if os.IsNotExist(err) {
            return "", nil
          }
          return "", err
        }
      GO
    end

    def build_app_server_binary!
      goarch = ENV.fetch("DEVOPSELLENCE_E2E_APP_GOARCH", capture!(go_binary, "env", "GOARCH", chdir: @gcp_mock_root.to_s).strip)
      run!(
        go_binary, "build",
        "-trimpath",
        "-o", "server",
        "server.go",
        chdir: @app_dir.to_s,
        timeout: 300,
        env: {
          "CGO_ENABLED" => "0",
          "GOOS" => "linux",
          "GOARCH" => goarch,
          "GOCACHE" => @state_dir.join(".gocache").to_s
        }
      )
    end

    def update_app_config!(plain_env_value, release_marker_value:)
      config_path = workspace_config_path
      config = YAML.load_file(config_path.to_s)
      config.delete("web")
      config.delete("release")
      config["services"] ||= {}
      config["services"]["web"] ||= { "kind" => "web", "roles" => [ "web" ] }
      config["services"]["web"]["kind"] = "web"
      config["services"]["web"]["roles"] = [ "web" ]
      config["services"]["web"]["ports"] = [ { "name" => "http", "port" => APP_PORT } ]
      config["services"]["web"]["healthcheck"] = { "path" => APP_HEALTH_PATH, "port" => APP_PORT }
      config["services"]["web"]["env"] ||= {}
      config["services"]["web"]["env"][PLAIN_ENV_NAME] = plain_env_value
      config["services"]["web"]["volumes"] = [ { "source" => "app_storage", "target" => APP_VOLUME_TARGET } ]
      config["tasks"] ||= {}
      config["tasks"]["release"] = {
        "service" => "web",
        "command" => "release-task #{APP_VOLUME_TARGET}/#{RELEASE_MARKER_FILENAME} #{Shellwords.escape(release_marker_value)}"
      }
      File.write(config_path, YAML.dump(config))
    end

    def workspace_config_path
      [
        @app_dir.join("config/devopsellence.yml"),
        @app_dir.join("devopsellence.yml")
      ].find(&:exist?) || @app_dir.join("devopsellence.yml")
    end

    def init_git_repo!
      run!("git", "init", chdir: @app_dir.to_s, timeout: 30, env: { "BUNDLE_GEMFILE" => nil })
      run!("git", "config", "user.name", "devopsellence e2e", chdir: @app_dir.to_s, timeout: 30, env: { "BUNDLE_GEMFILE" => nil })
      run!("git", "config", "user.email", "e2e@devopsellence.test", chdir: @app_dir.to_s, timeout: 30, env: { "BUNDLE_GEMFILE" => nil })
    end

    def commit_all!(message)
      run!("git", "add", ".", chdir: @app_dir.to_s, timeout: 30, env: { "BUNDLE_GEMFILE" => nil })
      status = capture!("git", "status", "--short", chdir: @app_dir.to_s)
      return if status.strip.empty?

      run!("git", "commit", "-m", message, chdir: @app_dir.to_s, timeout: 30, env: { "BUNDLE_GEMFILE" => nil })
    end

    def runner_mount_args
      [
        "-v", "#{@repo_mount_root}:/workspace:ro",
        "-v", "#{@state_mount_dir.join('control-plane/tmp')}:/workspace/tmp",
        "-v", "#{@state_mount_dir.join('control-plane/log')}:/workspace/log"
      ]
    end

    def runner_env_args
      stack_env.flat_map { |key, value| [ "-e", "#{key}=#{value}" ] }
    end

    def stack_env
      runtime_env
        .merge(ingress_env)
        .merge(gcp_mock_env)
        .merge(release_env)
        .merge(bundle_target_env)
    end

    def standalone_runtime?
      @runtime_backend == "standalone"
    end

    def runtime_env
      env = {
        "RAILS_ENV" => "development",
        "DATABASE_URL" => @internal_database_url,
        "RAILS_LOG_TO_STDOUT" => "1",
        "DEVOPSELLENCE_RUNTIME_BACKEND" => @runtime_backend,
        "DEVOPSELLENCE_PUBLIC_BASE_URL" => standalone_runtime? ? @internal_control_plane_base_url : @host_control_plane_base_url,
        "DEVOPSELLENCE_ALLOWED_HOSTS" => "web",
        "DEVOPSELLENCE_DEFAULT_GCP_PROJECT_ID" => RUNTIME_PROJECT,
        "DEVOPSELLENCE_DEFAULT_GCP_PROJECT_NUMBER" => RUNTIME_PROJECT_NUMBER,
        "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_POOL" => WORKLOAD_IDENTITY_POOL,
        "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_PROVIDER" => WORKLOAD_IDENTITY_PROVIDER,
        "DEVOPSELLENCE_DEFAULT_GAR_REGION" => RELEASE_REGION,
        "DEVOPSELLENCE_GCS_BUCKET_PREFIX" => RUNTIME_PROJECT,
        "DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_ID" => CONTROL_PLANE_SA_ID,
        "DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_PROJECT_ID" => CONTROL_PLANE_SA_PROJECT
      }
      if standalone_runtime?
        env.merge!(
          "DEVOPSELLENCE_SIGNING_BACKEND" => "local",
          "DEVOPSELLENCE_IDP_PRIVATE_KEY_PEM" => @idp_private_key_pem,
          "DEVOPSELLENCE_DESIRED_STATE_PRIVATE_KEY_PEM" => @desired_state_private_key_pem
        )
      else
        env.merge!(
          "DEVOPSELLENCE_SIGNING_BACKEND" => "gcp_kms",
          "DEVOPSELLENCE_IDP_SIGNING_KEY_VERSION" => IDP_SIGNING_KEY,
          "DEVOPSELLENCE_DESIRED_STATE_SIGNING_KEY_VERSION" => DESIRED_STATE_SIGNING_KEY
        )
      end
      env
    end

    def ingress_env
      {
        "DEVOPSELLENCE_INGRESS_BACKEND" => "local",
        "DEVOPSELLENCE_LOCAL_INGRESS_PUBLIC_URL" => @local_ingress_url,
        "DEVOPSELLENCE_LOCAL_INGRESS_HOSTNAME_SUFFIX" => "local.devopsellence.test"
      }
    end

    def gcp_mock_env
      {
        "DEVOPSELLENCE_GCP_FAKE_ACCESS_TOKEN" => "fake-control-plane-access-token",
        "DEVOPSELLENCE_GAR_HOST_OVERRIDE" => @host_registry
      }.merge(gcp_mock_endpoint_env(@internal_gcp_mock_base_url))
    end

    def gcp_mock_endpoint_env(base_url)
      {
        "DEVOPSELLENCE_GCS_ENDPOINT" => base_url,
        "DEVOPSELLENCE_STS_ENDPOINT" => "#{base_url}/sts/v1/token",
        "DEVOPSELLENCE_SECRET_MANAGER_ENDPOINT" => "#{base_url}/secretmanager/v1",
        "DEVOPSELLENCE_ARTIFACT_REGISTRY_ENDPOINT" => "#{base_url}/artifactregistry/v1",
        "DEVOPSELLENCE_ARTIFACT_REGISTRY_DOWNLOAD_ENDPOINT" => "#{base_url}/artifactregistry/download/v1",
        "DEVOPSELLENCE_IAM_ENDPOINT" => "#{base_url}/iam/v1",
        "DEVOPSELLENCE_IAM_CREDENTIALS_ENDPOINT" => "#{base_url}/iamcredentials/v1",
        "DEVOPSELLENCE_CLOUD_KMS_ENDPOINT" => "#{base_url}/cloudkms/v1"
      }
    end

    def release_env
      {
        "DEVOPSELLENCE_AGENT_STABLE_VERSION" => @release_version,
        "DEVOPSELLENCE_CLI_STABLE_VERSION" => @release_version
      }
    end

    def bundle_target_env
      {
        "DEVOPSELLENCE_MANAGED_POOL_TARGET" => "0",
        "DEVOPSELLENCE_ORGANIZATION_BUNDLE_TARGET" => "1",
        "DEVOPSELLENCE_ENVIRONMENT_BUNDLE_TARGET" => "1",
        "DEVOPSELLENCE_NODE_BUNDLE_TARGET" => "1"
      }
    end

    def cli_env
      {
        "DEVOPSELLENCE_BASE_URL" => @host_control_plane_base_url,
        "DEVOPSELLENCE_TOKEN" => @user_token
      }
    end

    def agent_gcp_args
      return [] if standalone_runtime?

      [
        "--gcs-api-endpoint", @internal_gcp_mock_base_url,
        "--secretmanager-endpoint", "#{@internal_gcp_mock_base_url}/secretmanager/v1",
        "--google-sts-endpoint", "#{@internal_gcp_mock_base_url}/sts/v1/token",
        "--google-iamcredentials-endpoint", "#{@internal_gcp_mock_base_url}/iamcredentials/v1"
      ]
    end

    def configure_registry_for_standalone!
      rails_eval!(<<~RUBY)
        project = Project.find_by!(name: #{@project_name.inspect})
        organization = project.organization
        config = organization.organization_registry_config || organization.build_organization_registry_config
        config.assign_attributes(
          registry_host: #{@host_registry.inspect},
          repository_namespace: #{"/e2e/#{@project_name}".sub(%r{\\A/+}, "").inspect},
          username: #{@registry_username.inspect},
          password: #{@registry_password.inspect}
        )
        config.save!
      RUBY
    end

    def cli_binary
      @cli_root.join("dist", @release_version, "linux-amd64")
    end

    def cli_json!(*args, timeout:)
      JSON.parse(run!(cli_binary.to_s, "--json", *args, chdir: @app_dir.to_s, timeout: timeout, env: cli_env))
    end

    def deploy_succeeded?(output)
      output.include?("rollout settled") || output.include?("[ok] Deploy complete.")
    end

    def go_binary
      @go_binary ||= resolved_go_binary
    end

    def agent_binary
      @agent_root.join("dist", @release_version, "linux-amd64")
    end

    def assert_artifact_redirect!(path, expected_location)
      response = http_get("#{@host_control_plane_base_url}#{path}")
      raise "expected redirect for #{path}, got #{response.fetch(:status)}" unless response.fetch(:status).between?(300, 399)
      raise "redirect location mismatch for #{path}" unless response.fetch(:location) == expected_location
    end

    def public_probe_url(base_url)
      URI.join(base_url.end_with?("/") ? base_url : "#{base_url}/", APP_PROBE_PATH.delete_prefix("/")).to_s
    end

    def wait_http_ok!(url, timeout:)
      deadline = Time.now + timeout
      loop do
        return if http_get(url).fetch(:status).between?(200, 299)

        raise "timed out waiting for #{url}" if Time.now >= deadline

        sleep 1
      rescue StandardError
        raise if Time.now >= deadline

        sleep 1
      end
    end

    def wait_until!(timeout:)
      deadline = Time.now + timeout
      loop do
        return if yield

        raise "timed out after #{timeout}s" if Time.now >= deadline

        sleep 1
      end
    end

    def rails_eval!(code)
      run!("docker", "exec", @web_container, "bin/rails", "runner", code, chdir: ROOT.to_s, timeout: 180)
    end

    def rails_json!(code)
      parse_json_output(capture!("docker", "exec", @web_container, "bin/rails", "runner", code, chdir: ROOT.to_s))
    end

    def run_runner!(command, timeout:)
      run!(
        "docker", "run", "--rm",
        "--network", @network,
        *docker_label_args,
        *runner_mount_args,
        *runner_env_args,
        @runner_image,
        "bash", "-lc", command,
        chdir: ROOT.to_s,
        timeout: timeout
      )
    end

    def docker_label_args
      @run_labels.flat_map { |key, value| [ "--label", "#{key}=#{value}" ] }
    end

    def docker_image_present?(image)
      system_success?("docker", "image", "inspect", image, chdir: ROOT.to_s)
    end

    def docker_container_running?(name)
      capture_optional!("docker", "inspect", "-f", "{{.State.Running}}", name, chdir: ROOT.to_s) == "true"
    end

    def docker_logs_excerpt(name, lines:)
      excerpt(capture_optional!("docker", "logs", name, chdir: ROOT.to_s), lines)
    end

    def docker_image_label(image, key)
      labels = docker_image_labels(image)
      labels.fetch(key, "")
    end

    def docker_image_labels(image)
      output = capture_optional!("docker", "image", "inspect", image, "--format", "{{json .Config.Labels}}", chdir: ROOT.to_s)
      return {} if output.empty? || output == "null"

      JSON.parse(output)
    rescue JSON::ParserError
      {}
    end

    def capture_logs!
      @container_log_paths.each do |container, path|
        output = capture!("docker", "logs", container, chdir: ROOT.to_s)
        File.write(path, output)
      rescue StandardError
        nil
      end
    end

    def teardown!
      capture_logs!
      if @keep_runtime
        puts "\n[keep] preserved e2e runtime"
        puts "[keep] network=#{@network}"
        puts "[keep] state_dir=#{@state_dir}"
        puts "[keep] app_dir=#{@app_dir}"
        puts "[keep] agent_state_dir=#{@agent_state_dir}"
        puts "[keep] repo_mount_root=#{@repo_mount_root}"
        return
      end

      cleanup_runtime!
    end

    def cleanup_runtime!
      container_ids = capture!("docker", "ps", "-aq", "--filter", "network=#{@network}", chdir: ROOT.to_s).lines.map(&:strip).reject(&:empty?)
      run!("docker", "rm", "-f", *container_ids, chdir: ROOT.to_s, timeout: 120) if container_ids.any?
      run!("docker", "network", "rm", @network, chdir: ROOT.to_s, timeout: 60)
      run!("docker", "image", "rm", "-f", @agent_image, @gcp_mock_image, chdir: ROOT.to_s, timeout: 60)
      FileUtils.rm_rf(@state_dir)
      FileUtils.rm_rf(@app_root_dir)
      FileUtils.rm_rf(@agent_state_dir.parent)
    rescue StandardError
      nil
    end

    def http_json(url)
      JSON.parse(http_get(url).fetch(:body))
    end

    def http_post_json(url, payload)
      uri = URI(url)
      request = Net::HTTP::Post.new(uri)
      request["Content-Type"] = "application/json"
      request.body = JSON.generate(payload)
      response = http_request(uri, request)
      raise "POST #{url} failed with #{response.code}: #{response.body}" unless response.code.to_i.between?(200, 299)

      response.body.to_s.strip.empty? ? {} : JSON.parse(response.body)
    end

    def http_get(url)
      uri = URI(url)
      request = Net::HTTP::Get.new(uri)
      response = http_request(uri, request)
      { status: response.code.to_i, body: response.body.to_s, location: response["Location"].to_s }
    end

    def http_request(uri, request)
      Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == "https", open_timeout: 5, read_timeout: 30) do |http|
        http.request(request)
      end
    end

    def resolve_checkout_root
      Pathname(capture!("git", "rev-parse", "--show-toplevel", chdir: ROOT.to_s).strip).expand_path
    end

    def resolve_workspace_root
      @checkout_root.parent
    end

    def resolve_repo_root(names, env_key)
      override = ENV[env_key].to_s.strip
      if override.empty?
        repo_root = repo_root_candidates(names).find(&:exist?)
        raise "#{names.join(' or ')} repo root not found under #{@workspace_root}" unless repo_root
      else
        repo_root = Pathname(override)
        raise "#{names.first} repo root not found: #{repo_root}" unless repo_root.exist?
      end

      repo_root
    end

    def repo_root_candidates(names)
      names.flat_map do |name|
        [
          @checkout_root.join(name),
          @checkout_root.join("ci-repos", name),
          @workspace_root.join(name)
        ]
      end
    end

    def available_port(start_port)
      port = start_port
      loop do
        return port unless tcp_port_open?("127.0.0.1", port)

        port += 1
      end
    end

    def tcp_port_open?(host, port)
      socket = TCPSocket.new(host, port)
      socket.close
      true
    rescue StandardError
      false
    end

    def system_success?(*cmd, chdir:)
      _stdout, _stderr, status = Open3.capture3(*cmd, chdir: chdir)
      status.success?
    end

    def resolved_go_binary
      override = ENV.fetch("DEVOPSELLENCE_E2E_GO_BIN", "").to_s.strip
      return override unless override.empty?

      configured_go_binary = capture_optional!("mise", "which", "go", chdir: @gcp_mock_root.to_s)
      return configured_go_binary unless configured_go_binary.empty?

      capture!("which", "go", chdir: ROOT.to_s).strip
    end

    def capture_optional!(*cmd, chdir:)
      output, _stderr, status = Open3.capture3(*cmd, chdir: chdir)
      return output.strip if status.success?

      ""
    end

    def run!(*cmd, chdir:, timeout:, env: {}, input: nil)
      result = run_command(*cmd, chdir:, timeout:, env:, input:)
      status = result.fetch(:status)
      unless status.success?
        raise "command failed (#{status.exitstatus}): #{Shellwords.join(cmd)}\n#{excerpt(result.fetch(:output), 20)}"
      end

      result.fetch(:output)
    end

    def run_command(*cmd, chdir:, timeout:, env: {}, input: nil)
      puts "$ #{Shellwords.join(cmd)}"
      output = +""
      Open3.popen2e(env, *cmd, chdir: chdir) do |stdin, stdout_and_stderr, wait_thread|
        stdin.write(input) if input
        stdin.close

        reader = Thread.new do
          stdout_and_stderr.each do |line|
            print line
            output << line
          end
        end

        status = nil
        begin
          Timeout.timeout(timeout) { status = wait_thread.value }
        rescue Timeout::Error
          Process.kill("TERM", wait_thread.pid) rescue nil
          sleep 2
          Process.kill("KILL", wait_thread.pid) rescue nil
          raise "timed out after #{timeout}s: #{Shellwords.join(cmd)}"
        ensure
          reader.join
        end

        { output: output, status: status }
      end
    end

    def capture!(*cmd, chdir:, env: {})
      stdout, stderr, status = Open3.capture3(env, *cmd, chdir: chdir)
      raise "command failed (#{status.exitstatus}): #{Shellwords.join(cmd)}\n#{stdout}#{stderr}" unless status.success?

      stdout
    end

    def excerpt(output, lines)
      output.to_s.lines.last(lines).join
    end

    def parse_json_output(output)
      line = output.to_s.lines.reverse.find { |entry| entry.to_s.strip.start_with?("{", "[") }
      JSON.parse(line || output.to_s)
    end
end

E2E.new.call
