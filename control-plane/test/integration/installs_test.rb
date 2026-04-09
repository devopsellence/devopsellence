# frozen_string_literal: true

require "test_helper"

class InstallsTest < ActionDispatch::IntegrationTest
  test "root page shows the cli install command for the serving host" do
    with_env(
      "DEVOPSELLENCE_HTTP_BASIC_USERNAME" => nil,
      "DEVOPSELLENCE_HTTP_BASIC_PASSWORD" => nil
    ) do
      https!
      host! "dev.devopsellence.com"

      get "/"

      assert_response :success
      assert_includes response.body, "curl -fsSL https://dev.devopsellence.com/lfg.sh | bash"
    end
  end

  test "public pages remain public when basic auth env vars are configured" do
    with_env(
      "DEVOPSELLENCE_HTTP_BASIC_USERNAME" => "friends",
      "DEVOPSELLENCE_HTTP_BASIC_PASSWORD" => "secret"
    ) do
      get "/"
      assert_response :success

      get "/login"
      assert_response :success
    end
  end

  test "cli install script remains public when basic auth is configured" do
    with_env(
      "DEVOPSELLENCE_HTTP_BASIC_USERNAME" => "friends",
      "DEVOPSELLENCE_HTTP_BASIC_PASSWORD" => "secret"
    ) do
      get "/lfg.sh"
    end

    assert_response :success
    assert_equal "text/plain", response.media_type
  end

  test "cli install script defaults base url to the serving host" do
    https!
    host! "dev.devopsellence.com"
    get "/lfg.sh"

    assert_response :success
    assert_equal "text/plain", response.media_type
    assert_includes response.body, 'BASE_URL="${DEVOPSELLENCE_BASE_URL:-https://dev.devopsellence.com}"'
    assert_includes response.body, 'INSTALL_DIR="${DEVOPSELLENCE_CLI_INSTALL_DIR:-}"'
    assert_includes response.body, 'if [[ "$OS" == "darwin" ]]; then'
    assert_includes response.body, 'INSTALL_DIR="$HOME/.local/bin"'
    assert_includes response.body, 'INSTALL_DIR="/usr/local/bin"'
    assert_includes response.body, "PATH_EXPORT='export PATH=\"'\"$INSTALL_DIR\"':$PATH\"'"
    assert_includes response.body, "echo '$PATH_EXPORT' >> $RC_FILE"
    assert_includes response.body, "source $RC_FILE"
  end

  test "cli install script ignores configured public base url when choosing default download host" do
    https!
    host! "dev.devopsellence.com"

    with_env("DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://app.devopsellence.com") do
      get "/lfg.sh"
    end

    assert_response :success
    assert_includes response.body, 'BASE_URL="${DEVOPSELLENCE_BASE_URL:-https://dev.devopsellence.com}"'
    refute_includes response.body, "https://app.devopsellence.com"
  end

  test "install script bootstraps docker on supported ubuntu releases" do
    get "/install.sh"

    assert_response :success
    assert_equal "text/plain", response.media_type
    assert_includes response.body, "install_docker_for_supported_ubuntu()"
    assert_includes response.body, "case \"${VERSION_CODENAME:-}\" in"
    assert_includes response.body, "jammy|noble)"
    assert_includes response.body, "https://download.docker.com/linux/ubuntu/gpg"
    assert_includes response.body, "/etc/apt/sources.list.d/docker.list"
    assert_includes response.body, "dpkg --print-architecture"
    assert_includes response.body, "docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin"
  end

  test "install script waits for docker at service startup" do
    get "/install.sh"

    assert_response :success
    assert_includes response.body, "After=network-online.target docker.service docker.socket"
    assert_includes response.body, "Wants=network-online.target docker.service docker.socket"
    assert_includes response.body, "ExecStart=$AGENT_BIN"
    refute_includes response.body, "ExecStart=$AGENT_BIN --mode=remote"
    assert_includes response.body, "Docker Engine is a prerequisite. Install and start Docker, then rerun this command."
  end

  test "install script downloads to a temp file before replacing the agent binary" do
    get "/install.sh"

    assert_response :success
    assert_includes response.body, "TMP_BIN=\"$(mktemp)\""
    assert_includes response.body, "trap cleanup EXIT"
    assert_includes response.body, "curl -fsSL \"$DOWNLOAD_URL\" -o \"$TMP_BIN\""
    assert_includes response.body, "run_root systemctl stop devopsellence-agent || true"
    assert_includes response.body, "run_root install -m 0755 \"$TMP_BIN\" \"$AGENT_BIN\""
  end

  test "uninstall script removes the agent and preserves runtime by default" do
    get "/uninstall.sh"

    assert_response :success
    assert_equal "text/plain", response.media_type
    assert_includes response.body, "PURGE_RUNTIME=0"
    assert_includes response.body, "run_root systemctl disable --now devopsellence-agent"
    assert_includes response.body, "run_root rm -f \"$SERVICE_FILE\""
    assert_includes response.body, "run_root rm -f \"$AGENT_BIN\""
    assert_includes response.body, "run_root rm -rf \"$ENV_DIR\""
    assert_includes response.body, "managed Docker runtime resources left intact; rerun with --purge-runtime to remove them."
  end

  test "uninstall script can purge managed docker runtime resources" do
    get "/uninstall.sh"

    assert_response :success
    assert_includes response.body, "--purge-runtime"
    assert_includes response.body, "docker ps -aq --filter label=devopsellence.managed=true"
    assert_includes response.body, "docker ps -aq --filter label=devopsellence.system"
    assert_includes response.body, "run_root docker rm -f \"${container_ids[@]}\""
    assert_includes response.body, "run_root docker network rm \"$NETWORK_NAME\" >/dev/null 2>&1 || true"
  end
end
