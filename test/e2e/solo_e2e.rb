#!/usr/bin/env ruby
# frozen_string_literal: true

# End-to-end test for solo mode (CLI + Agent, no control plane).
#
# Flow:
#   1. Build CLI + Agent binaries
#   2. Start a Docker container acting as the "remote node":
#      - OpenSSH server for CLI access
#      - Docker (via docker.sock mount)
#      - Fake systemctl/journalctl shims so CLI agent install can run in-container
#   3. Scaffold a test app with a solo-mode devopsellence.yml
#   4. Seed global solo state, attach the node, install the agent, set secrets,
#      deploy, check status
#   5. Assert: app container running, status.json terminal, secrets resolved
#
# Usage:
#   ruby test/e2e/solo_e2e.rb
#
# Environment:
#   DEVOPSELLENCE_E2E_RUN_ID            - unique run ID (auto-generated)
#   DEVOPSELLENCE_E2E_RELEASE_VERSION   - release version (auto-generated)
#   DEVOPSELLENCE_CLI_ROOT              - CLI repo root override
#   DEVOPSELLENCE_AGENT_ROOT            - Agent repo root override
#   DEVOPSELLENCE_E2E_KEEP=1            - preserve runtime after test
#   DEVOPSELLENCE_E2E_GO_BIN            - custom Go binary path
#   DEVOPSELLENCE_E2E_SSH_PORT          - custom SSH port for the node container
#   DEVOPSELLENCE_E2E_APP_TMPDIR        - parent directory for generated app/runtime files

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
require "uri"
require "yaml"
require_relative "binary_artifacts"

class MinimalHTTPServer
  def initialize(bind_address:, port:, handler:)
    @bind_address = bind_address
    @port = port
    @handler = handler
    @server = TCPServer.new(@bind_address, @port)
    @running = true
  end

  def start
    while @running
      begin
        client = @server.accept
      rescue IOError, Errno::EBADF
        break
      end
      Thread.new(client) { |socket| handle_client(socket) }
    end
  end

  def shutdown
    @running = false
    @server.close
  rescue IOError, Errno::EBADF
    nil
  end

  private

  Request = Struct.new(:path, :query, keyword_init: true)
  Response = Struct.new(:status, :headers, :body, :body_path, keyword_init: true)

  def handle_client(socket)
    request_line = socket.gets
    return if request_line.nil?

    method, target, _http_version = request_line.split(" ", 3)
    return write_response(socket, status: 405, body: "method not allowed\n") unless method == "GET"

    while (line = socket.gets)
      break if line == "\r\n"
    end

    uri = URI.parse(target)
    query = URI.decode_www_form(uri.query.to_s).to_h
    request = Request.new(path: uri.path, query: query)
    response = @handler.call(request)
    write_response(socket, status: response.status, headers: response.headers, body: response.body, body_path: response.body_path)
  rescue URI::InvalidURIError, ArgumentError
    write_response(socket, status: 400, body: "invalid request path\n")
  rescue IOError, Errno::EBADF
    nil
  ensure
    socket.close rescue nil
  end

  def write_response(socket, status:, body:, body_path: nil, headers: {})
    reason = {
      200 => "OK",
      400 => "Bad Request",
      404 => "Not Found",
      405 => "Method Not Allowed",
      500 => "Internal Server Error"
    }.fetch(status, "OK")

    payload = body.to_s.b
    content_length = body_path ? File.size(body_path).to_s : payload.bytesize.to_s

    socket.write "HTTP/1.1 #{status} #{reason}\r\n"
    merged_headers = {
      "Content-Length" => content_length,
      "Connection" => "close"
    }.merge(headers)
    merged_headers.each do |key, value|
      socket.write "#{key}: #{value}\r\n"
    end
    socket.write "\r\n"

    if body_path
      File.open(body_path, "rb") do |file|
        IO.copy_stream(file, socket)
      end
    else
      socket.write payload
    end
  end
end

class SoloE2E
  include E2EBinaryArtifacts

  ArtifactNotFoundError = Class.new(StandardError)

  MONOREPO_ROOT = Pathname(__dir__).join("../..").expand_path
  APP_PORT = 9292
  APP_HEALTH_PATH = "/up"
  APP_PROBE_PATH = "/e2e"
  SECRET_VALUE_NAME = "E2E_SECRET"
  PLAIN_ENV_NAME = "E2E_PLAIN_ENV"
  AGENT_RELEASE_PREFIX = "agent"
  RELEASE_VERSION_PATTERN = /\Av[0-9A-Za-z][0-9A-Za-z._-]*\z/
  RELEASE_TARGET_PATTERN = /\A[a-z0-9][a-z0-9_-]*\z/
  RELEASE_CHECKSUM_NAME = "SHA256SUMS"
  def initialize
    @run_id = ENV.fetch("DEVOPSELLENCE_E2E_RUN_ID", "").to_s.strip
    @run_id = "#{Time.now.utc.strftime('%Y%m%d%H%M%S')}-#{SecureRandom.hex(3)}" if @run_id.empty?
    @release_version = ENV.fetch("DEVOPSELLENCE_E2E_RELEASE_VERSION", "").to_s.strip
    @release_version = "v0.0.0-e2e.#{@run_id.tr('-', '.')}" if @release_version.empty?
    @checkout_root = resolve_checkout_root
    @workspace_root = @checkout_root.parent
    @cli_root = resolve_repo_root(%w[cli], "DEVOPSELLENCE_CLI_ROOT")
    @agent_root = resolve_repo_root(%w[agent], "DEVOPSELLENCE_AGENT_ROOT")
    @state_dir = MONOREPO_ROOT.join("test/e2e/tmp/solo", @run_id)
    @app_root_dir = app_tmp_root.join(@run_id)
    @agent_state_dir = @app_root_dir.join("node-state").to_s
    @desired_state_path = File.join(@agent_state_dir, "desired-state-override.json")
    @status_path = File.join(@agent_state_dir, "status.json")
    @envoy_bootstrap_path = File.join(@agent_state_dir, "envoy", "envoy.yaml")
    @app_dir = @app_root_dir.join("app")
    @log_dir = MONOREPO_ROOT.join("test/e2e/log")
    @image_build_dir = @state_dir.join("images")
    @ssh_port = Integer(ENV.fetch("DEVOPSELLENCE_E2E_SSH_PORT", available_port(20_000 + SecureRandom.random_number(10_000)).to_s))
    @artifact_server_port = available_port(31_000 + SecureRandom.random_number(1000))
    @network = "devopsellence-solo-e2e-#{@run_id}"
    @node_container = "devopsellence-solo-node-#{@run_id}"
    @node_image = "devopsellence/solo-e2e-node:#{@run_id}"
    @run_labels = {
      "devopsellence.e2e" => "1",
      "devopsellence.e2e.run_id" => @run_id,
      "devopsellence.e2e.mode" => "solo"
    }
    @keep_runtime = ENV["DEVOPSELLENCE_E2E_KEEP"] == "1"
    @container_log_paths = {
      @node_container => @log_dir.join("solo-e2e-node-#{@run_id}.log")
    }
    @ssh_key_dir = @state_dir.join("ssh")
    @ssh_client_home = @state_dir.join("ssh-home")
    @xdg_state_home = @state_dir.join("xdg-state")
    @project_name = "e2e-solo-#{SecureRandom.hex(3)}"
  end

  def call
    prepare_directories!

    step("prepare local binary artifacts") { prepare_binary_artifacts! }
    step("artifact server") { start_artifact_server! }
    step("build node image") { build_node_image! }
    step("generate SSH keys") { generate_ssh_keys! }
    step("network") { create_network! }
    step("start node") { start_node_container! }
    step("wait for node") { wait_for_node_ready! }
    step("scaffold app") { scaffold_app! }
    step("seed solo state") { seed_solo_state! }
    step("mode") { set_workspace_mode! }
    step("attach node") { attach_node! }
    step("install agent") { install_agent! }
    step("pre-deploy status") { assert_status_before_first_deploy! }
    step("secrets") { set_secrets! }
    step("deploy") { run_deploy! }
    step("assertions") { assert_runtime_state! }

    puts "\n[ok] solo e2e passed"
  ensure
    teardown!
  end

  private

  def app_tmp_root
    configured_root = ENV.fetch("DEVOPSELLENCE_E2E_APP_TMPDIR", "").to_s.strip
    return Pathname(configured_root).expand_path unless configured_root.empty?

    Pathname(Dir.tmpdir).join("devopsellence-solo-e2e-#{Process.uid}")
  end

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
    FileUtils.mkdir_p(@ssh_client_home.join(".ssh"))
    FileUtils.mkdir_p(@xdg_state_home)
  end

  # Build a Docker image that acts as a remote node:
  # - OpenSSH server for CLI SSH access
  # - Docker CLI (docker.sock mounted at runtime)
  # - Fake systemctl/journalctl so `devopsellence agent install` can exercise
  #   the real install path inside the container.
  def build_node_image!
    image_dir = @image_build_dir.join("node")
    FileUtils.rm_rf(image_dir)
    FileUtils.mkdir_p(image_dir)

    image_dir.join("entrypoint.sh").write(<<~SH)
      #!/bin/bash
      set -eu

      # Ensure state directory exists.
      mkdir -p #{@agent_state_dir}
      mkdir -p /var/run/devopsellence-fake-systemd
      touch /var/run/devopsellence-fake-systemd/devopsellence-agent.log

      # Copy the host-mounted public key into place so sshd StrictModes sees
      # root-owned authorized_keys even when the Docker host uses another uid.
      cp /tmp/devopsellence_authorized_key.pub /root/.ssh/authorized_keys
      chown root:root /root/.ssh/authorized_keys
      chmod 600 /root/.ssh/authorized_keys

      # Start sshd.
      mkdir -p /run/sshd
      /usr/sbin/sshd

      echo "[node] sshd started on port 22"
      echo "[node] waiting for CLI-managed agent install..."

      exec tail -f /dev/null
    SH

    image_dir.join("systemctl").write(<<~SH)
      #!/bin/bash
      set -euo pipefail

      STATE_DIR=/var/run/devopsellence-fake-systemd
      SERVICE_NAME=devopsellence-agent
      SERVICE_FILE=/etc/systemd/system/${SERVICE_NAME}.service
      PID_FILE="$STATE_DIR/${SERVICE_NAME}.pid"
      LOG_FILE="$STATE_DIR/${SERVICE_NAME}.log"

      mkdir -p "$STATE_DIR"
      touch "$LOG_FILE"

      cmd="${1:-}"
      shift || true

      service_matches() {
        case "${1:-}" in
          "$SERVICE_NAME"|"$SERVICE_NAME.service") return 0 ;;
          *) return 1 ;;
        esac
      }

      docker_matches() {
        case "${1:-}" in
          docker|docker.service) return 0 ;;
          *) return 1 ;;
        esac
      }

      read_exec_start() {
        sed -n 's/^ExecStart=//p' "$SERVICE_FILE" | head -n 1
      }

      launch_exec_start() {
        local exec_start="$1"
        EXEC_START="$exec_start" LOG_FILE="$LOG_FILE" python3 - <<'PY'
import os
import shlex
import subprocess
import sys

argv = [arg.replace("%%", "%") for arg in shlex.split(os.environ["EXEC_START"])]
if not argv:
    sys.exit("failed to parse ExecStart")

with open(os.environ["LOG_FILE"], "ab", buffering=0) as log_file:
    proc = subprocess.Popen(
        argv,
        stdout=log_file,
        stderr=subprocess.STDOUT,
        start_new_session=True
    )
print(proc.pid)
PY
      }

      service_pid_running() {
        [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null
      }

      case "$cmd" in
        daemon-reload|reset-failed)
          exit 0
          ;;
        enable)
          if [[ "${1:-}" == "--now" ]]; then
            shift
          fi
          unit="${1:-}"
          if docker_matches "$unit"; then
            exit 0
          fi
          service_matches "$unit"
          exec_start="$(read_exec_start)"
          [[ -n "$exec_start" ]]
          if service_pid_running; then
            exit 0
          fi
          service_pid="$(launch_exec_start "$exec_start")"
          echo "$service_pid" > "$PID_FILE"
          exit 0
          ;;
        stop)
          unit="${1:-}"
          if docker_matches "$unit"; then
            exit 0
          fi
          service_matches "$unit"
          if [[ -f "$PID_FILE" ]]; then
            kill "$(cat "$PID_FILE")" 2>/dev/null || true
            rm -f "$PID_FILE"
          fi
          exit 0
          ;;
        is-active)
          if [[ "${1:-}" == "--quiet" ]]; then
            shift
          fi
          unit="${1:-}"
          service_matches "$unit"
          if service_pid_running; then
            exit 0
          fi
          exit 3
          ;;
        show)
          unit="${@: -1}"
          service_matches "$unit"
          if service_pid_running; then
            printf 'ActiveState=active\nSubState=running\nResult=success\nExecMainStatus=0\n'
          else
            printf 'ActiveState=inactive\nSubState=dead\nResult=exit-code\nExecMainStatus=1\n'
          fi
          ;;
        status)
          unit="${@: -1}"
          service_matches "$unit"
          if service_pid_running; then
            echo "#{@run_id} $SERVICE_NAME active"
            exit 0
          fi
          echo "#{@run_id} $SERVICE_NAME inactive" >&2
          exit 3
          ;;
        *)
          echo "unsupported fake systemctl command: $cmd" >&2
          exit 1
          ;;
      esac
    SH

    image_dir.join("journalctl").write(<<~SH)
      #!/bin/bash
      set -euo pipefail

      LOG_FILE=/var/run/devopsellence-fake-systemd/devopsellence-agent.log
      follow=0
      lines=100

      while [[ $# -gt 0 ]]; do
        case "$1" in
          -f|--follow)
            follow=1
            shift
            ;;
          -n)
            lines="$2"
            shift 2
            ;;
          -u)
            shift 2
            ;;
          --no-pager)
            shift
            ;;
          *)
            shift
            ;;
        esac
      done

      if [[ "$follow" == "1" ]]; then
        exec tail -n "$lines" -f "$LOG_FILE"
      fi
      exec tail -n "$lines" "$LOG_FILE"
    SH

    image_dir.join("Dockerfile").write(<<~DOCKERFILE)
      FROM debian:bookworm-slim

      RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        gnupg \
        openssh-server \
        python3 \
        && install -m 0755 -d /etc/apt/keyrings \
        && curl -fsSL https://download.docker.com/linux/debian/gpg -o /tmp/docker.asc \
        && gpg --dearmor -o /etc/apt/keyrings/docker.gpg /tmp/docker.asc \
        && rm /tmp/docker.asc \
        && chmod a+r /etc/apt/keyrings/docker.gpg \
        && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian bookworm stable" > /etc/apt/sources.list.d/docker.list \
        && apt-get update \
        && apt-get install -y --no-install-recommends docker-ce-cli \
        && rm -rf /var/lib/apt/lists/*

      # Configure SSH: key-based auth only, no password.
      RUN mkdir -p /root/.ssh && chmod 700 /root/.ssh
      RUN sed -i 's/#PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config \
        && sed -i 's/#PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config \
        && sed -i 's/#PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config

      COPY entrypoint.sh /entrypoint.sh
      COPY systemctl /usr/local/bin/systemctl
      COPY journalctl /usr/local/bin/journalctl
      RUN chmod +x /entrypoint.sh /usr/local/bin/systemctl /usr/local/bin/journalctl

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
      "--add-host", "host.docker.internal:host-gateway",
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
  rescue StandardError => e
    puts "[debug] docker logs #{@node_container}:"
    puts capture_optional!("docker", "logs", @node_container, chdir: MONOREPO_ROOT.to_s)
    puts "[debug] last ssh output:"
    puts ssh_error
    raise e
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
      "schema_version" => 1,
      "organization" => "solo",
      "project" => @project_name,
      "default_environment" => "production",
      "build" => {
        "context" => ".",
        "dockerfile" => "Dockerfile",
        "platforms" => ["linux/#{app_goarch}"]
      },
      "services" => {
        "web" => {
          "ports" => [
            { "name" => "http", "port" => APP_PORT }
          ],
          "healthcheck" => {
            "path" => APP_HEALTH_PATH,
            "port" => APP_PORT
          },
          "env" => {
            PLAIN_ENV_NAME => "hello-solo"
          },
          "secret_refs" => [
            { "name" => SECRET_VALUE_NAME, "secret" => "projects/x/secrets/e2e" }
          ]
        }
      }
    }
    File.write(@app_dir.join("devopsellence.yml"), YAML.dump(config))
  end

  def write_app_files!
    @app_dir.join("Dockerfile").write(<<~DOCKERFILE)
      FROM alpine:3.22
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
    # Use CLI to set secrets and update devopsellence.yml secret_refs.
    run!(
      cli_binary.to_s, "secret", "set", SECRET_VALUE_NAME, "--service", "web", "--value", "secret-solo-123",
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
    secrets = parse_cli_json(output).fetch("secrets")
    raise "secret not listed" unless secrets.any? { |secret| secret["name"] == SECRET_VALUE_NAME }
    commit_all!("Configure solo e2e secrets")
    puts "[ok] Secret saved and listed"
  end

  def install_agent!
    output = run!(
      cli_binary.to_s, "agent", "install", "node-1",
      chdir: @app_dir.to_s,
      timeout: 180,
      env: ssh_env
    )

    result = parse_cli_json(output)
    raise "agent install did not report node-1" unless result["node"] == "node-1"
    puts "[ok] Agent installed via CLI"
  end

  def run_deploy!
    result = run_command(
      cli_binary.to_s, "deploy",
      chdir: @app_dir.to_s,
      timeout: 600,
      env: ssh_env
    )
    output = result.fetch(:output)
    status = result.fetch(:status)

    if status.success?
      result = parse_cli_json(output)
      raise "deploy did not report revision" if result["workload_revision"].to_s.empty?
      raise "deploy did not report node-1" unless Array(result["nodes"]).include?("node-1")
      puts "[ok] Deploy completed"
      return
    end

    unless output.include?("rollout failed on node-1:") && known_probe_error?(output)
      raise "deploy failed unexpectedly (#{status.exitstatus})\n#{excerpt(output, 20)}"
    end
    puts "[ok] Deploy surfaced known rollout failure in solo e2e"
  end

  def assert_status_before_first_deploy!
    cli_status_output = run!(
      cli_binary.to_s, "status",
      chdir: @app_dir.to_s,
      timeout: 60,
      env: ssh_env
    )
    cli_status = parse_cli_json(cli_status_output)
    node_status = (cli_status["nodes"] || []).find { |entry| entry["node"] == "node-1" }
    raise "CLI status missing node-1 before deploy" unless node_status
    raise "CLI status should not include runtime status before deploy" unless node_status["status"].nil?

    message = node_status["message"].to_s
    unless message.include?("no deploy status yet") && message.include?("devopsellence deploy")
      raise "unexpected pre-deploy status message: #{node_status.inspect}"
    end
    puts "[ok] CLI status reports no deploy status before first deploy"
  end

  def assert_runtime_state!
    desired = JSON.parse(ssh_to_node!("cat #{@desired_state_path}"))
    expected_runtime_revision = desired["revision"].to_s
    raise "desired state missing revision" if expected_runtime_revision.empty?
    environment_revisions = desired.fetch("environments").map { |environment| environment["revision"] }.compact.uniq
    raise "unexpected environment revisions: #{environment_revisions.inspect}" unless environment_revisions == [current_revision]
    workload_revision = environment_revisions.first

    # Wait for agent to reconcile and write status for the deployed revision.
    wait_until!(timeout: 120) do
      output = ssh_to_node!("cat #{@status_path} 2>/dev/null || echo '{}'")
      begin
        status = JSON.parse(output)
        status["revision"] == expected_runtime_revision && terminal_status_phase?(status["phase"])
      rescue JSON::ParserError
        false
      end
    end
    puts "[ok] Agent wrote status"

    # Read final status.
    status_output = ssh_to_node!("cat #{@status_path}")
    status = JSON.parse(status_output)
    revision = status["revision"]
    raise "revision missing from status" if revision.to_s.empty?
    raise "unexpected status revision: #{revision}" unless revision == expected_runtime_revision
    case status["phase"]
    when "settled"
      puts "[ok] Status settled for revision #{revision}"
    when "error"
      error = status["error"].to_s
      unless known_probe_error?(error)
        raise "unexpected error status: #{status.inspect}"
      end
      puts "[ok] Status captured known docker-sock probe limitation for revision #{revision}"
    else
      raise "unexpected phase: #{status['phase']}"
    end

    # Verify the web container exists in Docker.
    web_containers = []
    wait_until!(timeout: 60) do
      web_containers = ssh_to_node!(
        "docker ps --filter label=devopsellence.managed=true " \
        "--filter label=devopsellence.service=web " \
        "--filter label=devopsellence.revision=#{Shellwords.escape(workload_revision)} " \
        "--format '{{.Names}} {{.Status}}'"
      ).lines.map(&:strip).reject(&:empty?)
      web_containers.any?
    end
    raise "web container not running for revision #{workload_revision}" if web_containers.empty?
    puts "[ok] Web container running: #{web_containers.join(', ')}"

    # Verify via CLI status command.
    cli_status_result = run_command(
      cli_binary.to_s, "status",
      chdir: @app_dir.to_s,
      timeout: 60,
      env: ssh_env
    )
    cli_status = parse_cli_json(cli_status_result.fetch(:output))
    node_status = (cli_status["nodes"] || []).find { |entry| entry["node"] == "node-1" }
    raise "CLI status missing node-1" unless node_status
    cli_revision = node_status.dig("status", "revision")
    cli_phase = node_status.dig("status", "phase")
    raise "CLI status revision mismatch: #{cli_revision}" unless cli_revision == revision
    unless ["settled", "error"].include?(cli_phase)
      raise "CLI status phase unexpected: #{cli_phase.inspect}"
    end
    unless cli_status_result.fetch(:status).success? || (cli_phase == "error" && known_probe_error?(node_status.dig("status", "error").to_s))
      raise "CLI status failed unexpectedly (#{cli_status_result.fetch(:status).exitstatus}): #{excerpt(cli_status_result.fetch(:output), 20)}"
    end
    puts "[ok] CLI status confirms revision #{cli_revision} phase=#{cli_phase}"

    # Verify desired state was written to the correct path.
    services = desired.fetch("environments").flat_map { |environment| environment.fetch("services", []) }
    raise "desired state missing services" if services.empty?

    web_service = services.find { |service| service["name"] == "web" }
    raise "web service not in desired state" unless web_service
    raise "env #{PLAIN_ENV_NAME} missing" unless web_service.dig("env", PLAIN_ENV_NAME) == "hello-solo"
    raise "secret #{SECRET_VALUE_NAME} not resolved" unless web_service.dig("env", SECRET_VALUE_NAME) == "secret-solo-123"
    puts "[ok] Desired state verified: secrets resolved, env present"

    logs_output = run!(
      cli_binary.to_s, "node", "logs", "node-1",
      chdir: @app_dir.to_s,
      timeout: 30,
      env: ssh_env
    )
    logs_result = parse_cli_json(logs_output)
    log_lines = logs_result.fetch("lines")
    raise "node logs did not return agent log lines" if log_lines.empty?

    puts "[ok] Logs command returned #{log_lines.length} agent log lines"
  end

  def current_revision
    @current_revision ||= capture!("git", "rev-parse", "--short=7", "HEAD", chdir: @app_dir.to_s).strip
  end

  def terminal_status_phase?(phase)
    %w[settled error].include?(phase.to_s)
  end

  def known_probe_error?(text)
    text.include?("http probe") && text.include?("context deadline exceeded")
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
    # Disable the SSH agent and isolate known_hosts under a temp HOME so the
    # CLI uses the configured key without inheriting the user's SSH state.
    {
      "HOME" => @ssh_client_home.to_s,
      "SSH_AUTH_SOCK" => "",
      "DEVOPSELLENCE_BASE_URL" => artifact_base_url,
      "XDG_STATE_HOME" => @xdg_state_home.to_s
    }
  end

  def seed_solo_state!
    state_path = @xdg_state_home.join("devopsellence/solo/state.json")
    FileUtils.mkdir_p(state_path.dirname)
    state = {
      "schema_version" => 1,
      "nodes" => {
        "node-1" => {
          "labels" => ["web"],
          "host" => "127.0.0.1",
          "user" => "root",
          "port" => @ssh_port,
          "ssh_key" => @ssh_private_key.to_s,
          "agent_state_dir" => @agent_state_dir
        }
      }
    }
    state_path.write(JSON.pretty_generate(state))
    puts "[ok] Seeded solo state for node-1"
  end

  def set_workspace_mode!
    output = run!(
      cli_binary.to_s, "mode", "use", "solo",
      chdir: @app_dir.to_s,
      timeout: 30,
      env: ssh_env
    )
    result = parse_cli_json(output)
    raise "mode use solo did not confirm solo mode" unless result["mode"] == "solo"
    puts "[ok] Workspace mode set to solo"
  end

  def attach_node!
    output = run!(
      cli_binary.to_s, "node", "attach", "node-1",
      chdir: @app_dir.to_s,
      timeout: 30,
      env: ssh_env
    )
    result = parse_cli_json(output)
    unless result["node"] == "node-1" &&
           result["environment"] == "production" &&
           result["changed"]
      raise "node attach returned unexpected result: #{output}"
    end
    puts "[ok] Solo node attached"
  end


  def parse_cli_json(output)
    starts = []
    output.to_s.each_char.with_index { |char, index| starts << index if char == "{" }
    starts.reverse_each do |index|
      begin
        return JSON.parse(output[index..].strip)
      rescue JSON::ParserError
        next
      end
    end
    raise "CLI output did not contain a JSON object: #{excerpt(output, 20)}"
  end

  def cli_binary
    @cli_root.join("dist", @release_version, "linux-amd64")
  end

  def agent_binary
    @agent_root.join("dist", @release_version, "linux-amd64")
  end

  def artifact_base_url
    "http://host.docker.internal:#{@artifact_server_port}"
  end

  def start_artifact_server!
    @artifact_server = MinimalHTTPServer.new(
      bind_address: "0.0.0.0",
      port: @artifact_server_port,
      handler: method(:route_artifact_request)
    )
    @artifact_server_thread = Thread.new { @artifact_server.start }
    wait_until!(timeout: 10) { tcp_port_open?("127.0.0.1", @artifact_server_port) }
    puts "[ok] Artifact server listening at #{artifact_base_url}"
  end

  def route_artifact_request(req)
    version = req.query["version"].to_s.strip
    version = @release_version if version.empty?

    case req.path
    when "/agent/download"
      os = validate_artifact_component!("os", req.query.fetch("os"), RELEASE_TARGET_PATTERN)
      arch = validate_artifact_component!("arch", req.query.fetch("arch"), RELEASE_TARGET_PATTERN)
      artifact = release_artifact_path(@agent_root, version, "#{os}-#{arch}")
      file_response(status: 200, path: artifact, content_type: "application/octet-stream")
    when "/agent/checksums"
      checksums = release_artifact_path(@agent_root, version, RELEASE_CHECKSUM_NAME)
      response(status: 200, body: prefixed_checksums(checksums, AGENT_RELEASE_PREFIX), content_type: "text/plain; charset=utf-8")
    else
      response(status: 404, body: "not found\n")
    end
  rescue KeyError => e
    response(status: 400, body: "missing query param: #{e.message}\n")
  rescue ArgumentError => e
    response(status: 400, body: "#{e.message}\n")
  rescue ArtifactNotFoundError => e
    response(status: 404, body: "#{e.message}\n")
  rescue StandardError => e
    response(status: 500, body: "#{e.class}: #{e.message}\n")
  end

  def release_artifact_path(root, version, name)
    validated_version = validate_artifact_component!("version", version, RELEASE_VERSION_PATTERN)
    validated_name =
      if name == RELEASE_CHECKSUM_NAME
        name
      else
        validate_artifact_component!("target", name, RELEASE_TARGET_PATTERN)
      end

    dist_root = Pathname(root).join("dist").expand_path
    path = dist_root.join(validated_version, validated_name).expand_path
    raise ArgumentError, "invalid artifact path" unless path.to_s.start_with?(dist_root.to_s + File::SEPARATOR)
    raise ArtifactNotFoundError, "artifact not found" unless path.file?

    path
  end

  def validate_artifact_component!(name, value, pattern)
    value = value.to_s.strip
    raise ArgumentError, "invalid #{name}" unless pattern.match?(value)

    value
  end

  def response(status:, body:, content_type: nil)
    headers = {}
    headers["Content-Type"] = content_type if content_type
    MinimalHTTPServer::Response.new(status: status, headers: headers, body: body)
  end

  def file_response(status:, path:, content_type: nil)
    headers = {}
    headers["Content-Type"] = content_type if content_type
    MinimalHTTPServer::Response.new(status: status, headers: headers, body_path: path.to_s)
  end

  def prefixed_checksums(path, prefix)
    File.read(path).lines.map do |line|
      checksum, filename = line.strip.split(/\s+/, 2)
      next line if checksum.to_s.empty? || filename.to_s.empty?

      "#{checksum}  #{prefix}-#{filename}\n"
    end.join
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
    stop_artifact_server!
    if @keep_runtime
      puts "\n[keep] preserved solo e2e runtime"
      puts "[keep] network=#{@network}"
      puts "[keep] state_dir=#{@state_dir}"
      puts "[keep] app_dir=#{@app_dir}"
      puts "[keep] ssh_port=#{@ssh_port}"
      puts "[keep] ssh_key=#{@ssh_private_key}"
      return
    end

    cleanup_runtime!
  end

  def stop_artifact_server!
    return unless @artifact_server

    @artifact_server.shutdown
    @artifact_server_thread&.join(5)
    @artifact_server = nil
    @artifact_server_thread = nil
  rescue StandardError
    nil
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

SoloE2E.new.call
