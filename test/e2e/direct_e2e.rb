#!/usr/bin/env ruby
# frozen_string_literal: true

# End-to-end test for solo mode (CLI + Agent, no control plane).
#
# Flow:
#   1. Build CLI + Agent binaries
#   2. Start a Docker container acting as the "remote node":
#      - OpenSSH server for CLI access
#      - Docker (via docker.sock mount)
#      - Agent in --mode=direct watching desired-state file
#   3. Scaffold a test Go app with devopsellence.yml (solo config under `direct`)
#   4. CLI sets secrets, deploys, checks status
#   5. Assert: app container running, status.json settled, secrets resolved
#
# Usage:
#   ruby test/e2e/direct_e2e.rb
#
# Environment:
#   DEVOPSELLENCE_E2E_RUN_ID            - unique run ID (auto-generated)
#   DEVOPSELLENCE_E2E_RELEASE_VERSION   - release version (auto-generated)
#   DEVOPSELLENCE_CLI_ROOT              - CLI repo root override
#   DEVOPSELLENCE_AGENT_ROOT            - Agent repo root override
#   DEVOPSELLENCE_E2E_KEEP=1            - preserve runtime after test
#   DEVOPSELLENCE_E2E_GO_BIN            - custom Go binary path
#   DEVOPSELLENCE_E2E_SSH_PORT          - custom SSH port for the node container

require "digest"
require "fileutils"
require "json"
require "open3"
require "pathname"
require "securerandom"
require "shellwords"
require "socket"
require "time"
require "timeout"
require "tmpdir"
require "yaml"
require_relative "binary_artifacts"

class DirectE2E
  include E2EBinaryArtifacts

  MONOREPO_ROOT = Pathname(__dir__).join("../..").expand_path
  APP_PORT = 9292
  APP_HEALTH_PATH = "/up"
  APP_PROBE_PATH = "/e2e"
  SECRET_VALUE_NAME = "E2E_SECRET"
  PLAIN_ENV_NAME = "E2E_PLAIN_ENV"
  def initialize
    @run_id = ENV.fetch("DEVOPSELLENCE_E2E_RUN_ID", "").to_s.strip
    @run_id = "#{Time.now.utc.strftime('%Y%m%d%H%M%S')}-#{SecureRandom.hex(3)}" if @run_id.empty?
    @release_version = ENV.fetch("DEVOPSELLENCE_E2E_RELEASE_VERSION", "").to_s.strip
    @release_version = "v0.0.0-e2e.#{@run_id.tr('-', '.')}" if @release_version.empty?
    @checkout_root = resolve_checkout_root
    @workspace_root = @checkout_root.parent
    @cli_root = resolve_repo_root(%w[cli], "DEVOPSELLENCE_CLI_ROOT")
    @agent_root = resolve_repo_root(%w[agent], "DEVOPSELLENCE_AGENT_ROOT")
    @state_dir = MONOREPO_ROOT.join("test/e2e/tmp/direct", @run_id)
    @app_root_dir = Pathname(Dir.tmpdir).join("devopsellence-direct-e2e", @run_id)
    @agent_state_dir = @app_root_dir.join("node-state").to_s
    @desired_state_path = File.join(@agent_state_dir, "desired-state-override.json")
    @status_path = File.join(@agent_state_dir, "status.json")
    @envoy_bootstrap_path = File.join(@agent_state_dir, "envoy", "envoy.yaml")
    @app_dir = @app_root_dir.join("app")
    @log_dir = MONOREPO_ROOT.join("test/e2e/log")
    @image_build_dir = @state_dir.join("images")
    @ssh_port = Integer(ENV.fetch("DEVOPSELLENCE_E2E_SSH_PORT", available_port(12_200).to_s))
    @network = "devopsellence-direct-e2e-#{@run_id}"
    @node_container = "devopsellence-direct-node-#{@run_id}"
    @node_image = "devopsellence/direct-e2e-node:#{@run_id}"
    @run_labels = {
      "devopsellence.e2e" => "1",
      "devopsellence.e2e.run_id" => @run_id,
      "devopsellence.e2e.mode" => "direct"
    }
    @keep_runtime = ENV["DEVOPSELLENCE_E2E_KEEP"] == "1"
    @container_log_paths = {
      @node_container => @log_dir.join("direct-e2e-node-#{@run_id}.log")
    }
    @ssh_key_dir = @state_dir.join("ssh")
    @project_name = "e2e-direct-#{SecureRandom.hex(3)}"
  end

  def call
    prepare_directories!

    step("prepare local binary artifacts") { prepare_binary_artifacts! }
    step("build node image") { build_node_image! }
    step("generate SSH keys") { generate_ssh_keys! }
    step("network") { create_network! }
    step("start node") { start_node_container! }
    step("wait for node") { wait_for_node_ready! }
    step("scaffold app") { scaffold_app! }
    step("mode") { set_workspace_mode! }
    step("secrets") { set_secrets! }
    step("deploy") { run_deploy! }
    step("assertions") { assert_runtime_state! }

    puts "\n[ok] solo e2e passed"
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
    FileUtils.mkdir_p(@agent_state_dir)
    FileUtils.mkdir_p(@app_dir)
    FileUtils.mkdir_p(@image_build_dir)
    FileUtils.mkdir_p(@ssh_key_dir)
  end

  # Build a Docker image that acts as a remote node:
  # - OpenSSH server for CLI SSH access
  # - Docker CLI (docker.sock mounted at runtime)
  # - Agent binary running in the solo-path `--mode=direct`
  def build_node_image!
    image_dir = @image_build_dir.join("node")
    FileUtils.rm_rf(image_dir)
    FileUtils.mkdir_p(image_dir)

    FileUtils.cp(agent_binary, image_dir.join("devopsellence"))

    image_dir.join("entrypoint.sh").write(<<~SH)
      #!/bin/bash
      set -eu

      # Ensure state directory exists.
      mkdir -p #{@agent_state_dir}

      # Copy the host-mounted public key into place so sshd StrictModes sees
      # root-owned authorized_keys even when the Docker host uses another uid.
      cp /tmp/devopsellence_authorized_key.pub /root/.ssh/authorized_keys
      chown root:root /root/.ssh/authorized_keys
      chmod 600 /root/.ssh/authorized_keys

      # Start sshd.
      mkdir -p /run/sshd
      /usr/sbin/sshd

      echo "[node] sshd started on port 22"
      echo "[node] starting agent in solo mode..."

      # Run agent in the solo-path implementation.
      exec /usr/local/bin/devopsellence \
        --mode=direct \
        --auth-state-path=#{@agent_state_dir}/auth.json \
        --desired-state-override-path=#{@desired_state_path} \
        --envoy-bootstrap-path=#{@envoy_bootstrap_path} \
        --network=#{@network} \
        --prefetch-system-images=false
    SH

    image_dir.join("Dockerfile").write(<<~DOCKERFILE)
      FROM debian:bookworm-slim

      RUN apt-get update && apt-get install -y --no-install-recommends \
        openssh-server \
        docker.io \
        ca-certificates \
        && rm -rf /var/lib/apt/lists/*

      # Configure SSH: key-based auth only, no password.
      RUN mkdir -p /root/.ssh && chmod 700 /root/.ssh
      RUN sed -i 's/#PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config \
        && sed -i 's/#PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config \
        && sed -i 's/#PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config

      COPY devopsellence /usr/local/bin/devopsellence
      COPY entrypoint.sh /entrypoint.sh
      RUN chmod +x /entrypoint.sh /usr/local/bin/devopsellence

      EXPOSE 22
      ENTRYPOINT ["/entrypoint.sh"]
    DOCKERFILE

    run!("docker", "build", "-t", @node_image, image_dir.to_s, chdir: MONOREPO_ROOT.to_s, timeout: 600)
  end

  def generate_ssh_keys!
    key_path = @ssh_key_dir.join("id_ed25519")
    run!(
      "ssh-keygen", "-t", "ed25519", "-f", key_path.to_s, "-N", "", "-q",
      chdir: @ssh_key_dir.to_s, timeout: 30
    )
    @ssh_private_key = key_path
    @ssh_public_key = Pathname("#{key_path}.pub")
  end

  def create_network!
    run!("docker", "network", "create", *docker_label_args, @network, chdir: MONOREPO_ROOT.to_s, timeout: 60)
  end

  def start_node_container!
    run!(
      "docker", "run", "-d", "--rm",
      "--name", @node_container,
      "--network", @network,
      "--network-alias", "node",
      *docker_label_args,
      "-p", "127.0.0.1:#{@ssh_port}:22",
      "-v", "/var/run/docker.sock:/var/run/docker.sock",
      "-v", "#{@agent_state_dir}:#{@agent_state_dir}",
      "-v", "#{@ssh_public_key}:/tmp/devopsellence_authorized_key.pub:ro",
      @node_image,
      chdir: MONOREPO_ROOT.to_s,
      timeout: 120
    )
  end

  def wait_for_node_ready!
    ssh_error = +""
    # Wait for sshd to accept connections.
    wait_until!(timeout: 60) do
      result = run_silent(
        "ssh",
        "-o", "BatchMode=yes",
        "-o", "StrictHostKeyChecking=no",
        "-o", "UserKnownHostsFile=/dev/null",
        "-o", "ConnectTimeout=2",
        "-i", @ssh_private_key.to_s,
        "-p", @ssh_port.to_s,
        "root@127.0.0.1",
        "echo ok",
        chdir: MONOREPO_ROOT.to_s
      )
      ssh_error = result.fetch(:output)
      result.fetch(:status).success?
    end
    puts "[ok] SSH connection established"
    wait_for_agent_ready!
  rescue StandardError => e
    puts "[debug] docker logs #{@node_container}:"
    puts capture_optional!("docker", "logs", @node_container, chdir: MONOREPO_ROOT.to_s)
    puts "[debug] last ssh output:"
    puts ssh_error
    raise e
  end

  def wait_for_agent_ready!
    # Wait for agent to be running (it writes a log line).
    wait_until!(timeout: 60) do
      output = capture_optional!("docker", "logs", @node_container, chdir: MONOREPO_ROOT.to_s)
      output.include?("starting agent in solo mode")
    end
    puts "[ok] Agent started in solo mode"
  end

  def scaffold_app!
    FileUtils.mkdir_p(@app_dir)
    init_git_repo!
    write_devopsellence_yml!
    write_app_files!
    build_app_server_binary!
    commit_all!("Initialize solo e2e app")
  end

  def write_devopsellence_yml!
    config = {
      "schema_version" => 3,
      "organization" => "e2e-org",
      "project" => @project_name,
      "build" => {
        "context" => ".",
        "dockerfile" => "Dockerfile",
        "platforms" => ["linux/#{app_goarch}"]
      },
      "web" => {
        "command" => "/server",
        "port" => APP_PORT,
        "healthcheck" => {
          "path" => APP_HEALTH_PATH,
          "port" => APP_PORT
        },
        "env" => {
          PLAIN_ENV_NAME => "hello-direct"
        },
        "secret_refs" => [
          { "name" => SECRET_VALUE_NAME, "secret" => "projects/x/secrets/e2e" }
        ]
      },
      "direct" => {
        "nodes" => {
          "node-1" => {
            "host" => "127.0.0.1",
            "user" => "root",
            "port" => @ssh_port,
            "ssh_key" => @ssh_private_key.to_s,
            "agent_state_dir" => @agent_state_dir,
            "labels" => ["web"]
          }
        }
      }
    }
    File.write(@app_dir.join("devopsellence.yml"), YAML.dump(config))
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
        "log"
        "net/http"
        "os"
      )

      func main() {
        mux := http.NewServeMux()
        mux.HandleFunc("#{APP_HEALTH_PATH}", func(w http.ResponseWriter, r *http.Request) {
          _, _ = w.Write([]byte("ok"))
        })
        mux.HandleFunc("#{APP_PROBE_PATH}", func(w http.ResponseWriter, r *http.Request) {
          payload := map[string]string{
            "plain_env":    os.Getenv("#{PLAIN_ENV_NAME}"),
            "secret_value": os.Getenv("#{SECRET_VALUE_NAME}"),
          }
          w.Header().Set("Content-Type", "application/json")
          _ = json.NewEncoder(w).Encode(payload)
        })

        if err := http.ListenAndServe(":#{APP_PORT}", mux); err != nil {
          log.Fatal(err)
        }
      }
    GO
  end

  def build_app_server_binary!
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
        "GOARCH" => app_goarch,
        "GOCACHE" => @state_dir.join(".gocache").to_s
      }
    )
  end

  def set_secrets!
    # Use CLI to set secrets (writes to .env in app dir).
    run!(
      cli_binary.to_s, "secret", "set", SECRET_VALUE_NAME, "--value", "secret-direct-123",
      chdir: @app_dir.to_s,
      timeout: 30,
      env: ssh_env
    )

    # Verify the secret was saved.
    output = run!(
      cli_binary.to_s, "secret", "list",
      chdir: @app_dir.to_s,
      timeout: 30,
      env: ssh_env
    )
    raise "secret not listed" unless output.include?(SECRET_VALUE_NAME)
    puts "[ok] Secret saved and listed"
  end

  def run_deploy!
    output = run!(
      cli_binary.to_s, "deploy",
      chdir: @app_dir.to_s,
      timeout: 600,
      env: ssh_env
    )

    raise "deploy did not report success" unless output.include?("Deployed revision")
    puts "[ok] Deploy completed"
  end

  def assert_runtime_state!
    # Wait for agent to reconcile and write status.
    wait_until!(timeout: 120) do
      output = ssh_to_node!("cat #{@status_path} 2>/dev/null || echo '{}'")
      begin
        status = JSON.parse(output)
        status["phase"] == "settled"
      rescue JSON::ParserError
        false
      end
    end
    puts "[ok] Agent settled"

    # Read final status.
    status_output = ssh_to_node!("cat #{@status_path}")
    status = JSON.parse(status_output)
    raise "unexpected phase: #{status['phase']}" unless status["phase"] == "settled"

    revision = status["revision"]
    raise "revision missing from status" if revision.to_s.empty?
    puts "[ok] Status: phase=#{status['phase']} revision=#{revision}"

    # Verify the web container exists in Docker.
    web_containers = ssh_to_node!(
      "docker ps --filter label=devopsellence.managed=true " \
      "--filter label=devopsellence.service=web " \
      "--filter label=devopsellence.revision=#{Shellwords.escape(revision)} " \
      "--format '{{.Names}} {{.Status}}'"
    ).lines.map(&:strip).reject(&:empty?)
    raise "web container not running for revision #{revision}" if web_containers.empty?
    puts "[ok] Web container running: #{web_containers.join(', ')}"

    # Verify via CLI status command.
    cli_status_output = run!(
      cli_binary.to_s, "--json", "status",
      chdir: @app_dir.to_s,
      timeout: 60,
      env: ssh_env
    )
    cli_status = JSON.parse(cli_status_output)
    node_status = (cli_status["nodes"] || []).find { |entry| entry["node"] == "node-1" }
    raise "CLI status phase not settled" unless node_status&.dig("status", "phase") == "settled"
    puts "[ok] CLI status confirms settled"

    # Verify desired state was written to the correct path.
    ds_output = ssh_to_node!("cat #{@desired_state_path}")
    desired = JSON.parse(ds_output)
    raise "desired state missing revision" if desired["revision"].to_s.empty?
    raise "desired state missing containers" if (desired["containers"] || []).empty?

    web_container = desired["containers"].find { |c| c["serviceName"] == "web" }
    raise "web container not in desired state" unless web_container
    raise "env #{PLAIN_ENV_NAME} missing" unless web_container.dig("env", PLAIN_ENV_NAME) == "hello-direct"
    raise "secret #{SECRET_VALUE_NAME} not resolved" unless web_container.dig("env", SECRET_VALUE_NAME) == "secret-direct-123"
    puts "[ok] Desired state verified: secrets resolved, env present"

    # Verify CLI logs command runs (may fail inside test container due to no systemd,
    # but verify the SSH connection and command dispatch works).
    begin
      logs_output = run!(
        cli_binary.to_s, "node", "logs", "node-1",
        chdir: @app_dir.to_s,
        timeout: 30,
        env: ssh_env
      )
      puts "[ok] Logs command succeeded (#{logs_output.lines.length} lines)"
    rescue StandardError => e
      # journalctl may not be available in the test container — that's expected.
      puts "[skip] Logs command failed (expected in test env): #{e.message.lines.first}"
    end
  end

  # -- Helpers --

  def ssh_to_node!(command)
    capture!(
      "ssh",
      "-o", "BatchMode=yes",
      "-o", "StrictHostKeyChecking=no",
      "-o", "UserKnownHostsFile=/dev/null",
      "-i", @ssh_private_key.to_s,
      "-p", @ssh_port.to_s,
      "root@127.0.0.1",
      command,
      chdir: MONOREPO_ROOT.to_s
    )
  end

  def ssh_env
    # Disable SSH agent so CLI uses the key from config (ssh_key field).
    # Also disable strict host key checking for known_hosts noise.
    { "SSH_AUTH_SOCK" => "" }
  end

  def set_workspace_mode!
    output = run!(
      cli_binary.to_s, "mode", "use", "solo",
      chdir: @app_dir.to_s,
      timeout: 30,
      env: ssh_env
    )
    raise "mode use solo did not confirm solo mode" unless output.include?("Mode: solo")
    puts "[ok] Workspace mode set to solo"
  end

  def cli_binary
    @cli_root.join("dist", @release_version, "linux-amd64")
  end

  def agent_binary
    @agent_root.join("dist", @release_version, "linux-amd64")
  end

  def docker_label_args
    @run_labels.flat_map { |key, value| ["--label", "#{key}=#{value}"] }
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

  def go_binary
    @go_binary ||= begin
      override = ENV.fetch("DEVOPSELLENCE_E2E_GO_BIN", "").to_s.strip
      return override unless override.empty?

      [@cli_root, @agent_root, MONOREPO_ROOT].each do |root|
        configured = capture_optional!("mise", "which", "go", chdir: root.to_s)
        return configured unless configured.empty?
      end

      capture!("which", "go", chdir: MONOREPO_ROOT.to_s).strip
    end
  end

  def app_goarch
    @app_goarch ||= begin
      override = ENV.fetch("DEVOPSELLENCE_E2E_APP_GOARCH", "").to_s.strip
      return override unless override.empty?

      capture!(go_binary, "env", "GOARCH", chdir: @app_dir.to_s).strip
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

  def wait_until!(timeout:)
    deadline = Time.now + timeout
    loop do
      return if yield

      raise "timed out after #{timeout}s" if Time.now >= deadline

      sleep 1
    end
  end

  def system_success?(*cmd, chdir:)
    _stdout, _stderr, status = Open3.capture3(*cmd, chdir: chdir)
    status.success?
  end

  def run_silent(*cmd, chdir:, env: {})
    output, stderr, status = Open3.capture3(env, *cmd, chdir: chdir)
    { output: "#{output}#{stderr}", status: status }
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

  def capture_optional!(*cmd, chdir:)
    output, _stderr, status = Open3.capture3(*cmd, chdir: chdir)
    return output.strip if status.success?

    ""
  end

  def excerpt(output, lines)
    output.to_s.lines.last(lines).join
  end

  def capture_logs!
    @container_log_paths.each do |container, path|
      output = capture!("docker", "logs", container, chdir: MONOREPO_ROOT.to_s)
      File.write(path, output)
    rescue StandardError
      nil
    end
  end

  def teardown!
    capture_logs!
    if @keep_runtime
      puts "\n[keep] preserved direct e2e runtime"
      puts "[keep] network=#{@network}"
      puts "[keep] state_dir=#{@state_dir}"
      puts "[keep] app_dir=#{@app_dir}"
      puts "[keep] ssh_port=#{@ssh_port}"
      puts "[keep] ssh_key=#{@ssh_private_key}"
      return
    end

    cleanup_runtime!
  end

  def cleanup_runtime!
    container_ids = capture!("docker", "ps", "-aq", "--filter", "network=#{@network}", chdir: MONOREPO_ROOT.to_s).lines.map(&:strip).reject(&:empty?)
    run!("docker", "rm", "-f", *container_ids, chdir: MONOREPO_ROOT.to_s, timeout: 120) if container_ids.any?
    run!("docker", "network", "rm", @network, chdir: MONOREPO_ROOT.to_s, timeout: 60)
    run!("docker", "image", "rm", "-f", @node_image, chdir: MONOREPO_ROOT.to_s, timeout: 60)
    FileUtils.rm_rf(@state_dir)
    FileUtils.rm_rf(@app_root_dir)
  rescue StandardError => e
    puts "[warn] cleanup error: #{e.message}"
  end

  def resolve_checkout_root
    Pathname(capture!("git", "rev-parse", "--show-toplevel", chdir: MONOREPO_ROOT.to_s).strip).expand_path
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
end

DirectE2E.new.call
