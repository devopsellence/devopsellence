# frozen_string_literal: true

require "digest"
require "fileutils"
require "open3"
require "tmpdir"
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
    assert_includes response.body, 'BASE_URL="${DEVOPSELLENCE_BASE_URL:-}"'
    assert_includes response.body, "BASE_URL='https://dev.devopsellence.com'"
    assert_includes response.body, 'INSTALL_DIR="${DEVOPSELLENCE_CLI_INSTALL_DIR:-}"'
    assert_includes response.body, 'INSTALL_AGENT_SKILL="${DEVOPSELLENCE_INSTALL_AGENT_SKILL:-}"'
    assert_includes response.body, "INSTALL_SCRIPT_URL='https://dev.devopsellence.com/lfg.sh'"
    assert_includes response.body, "--install-agent-skill"
    assert_includes response.body, 'INSTALL_DIR="$HOME/.local/bin"'
    refute_includes response.body, 'INSTALL_DIR="/usr/local/bin"'
    assert_includes response.body, "PATH_EXPORT='export PATH=\"'\"$INSTALL_DIR\"':$PATH\"'"
    assert_includes response.body, "echo '$PATH_EXPORT' >> $RC_FILE"
    assert_includes response.body, "source $RC_FILE"
    assert_includes response.body, "npx skills add devopsellence/devopsellence --skill devopsellence -g"
    assert_includes response.body, 'curl -fsSL "$INSTALL_SCRIPT_URL?version=$CLI_VERSION" | bash -s -- --install-agent-skill'
  end

  test "cli install script ignores configured public base url when choosing default download host" do
    https!
    host! "dev.devopsellence.com"

    with_env("DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://app.devopsellence.com") do
      get "/lfg.sh"
    end

    assert_response :success
    assert_includes response.body, 'BASE_URL="${DEVOPSELLENCE_BASE_URL:-}"'
    assert_includes response.body, "BASE_URL='https://dev.devopsellence.com'"
    assert_includes response.body, "INSTALL_SCRIPT_URL='https://dev.devopsellence.com/lfg.sh'"
    refute_includes response.body, "https://app.devopsellence.com"
  end

  test "cli install script skill rerun command keeps script host separate from download override" do
    https!
    host! "dev.devopsellence.com"
    get "/lfg.sh"

    assert_response :success
    assert_includes response.body, "BASE_URL='https://dev.devopsellence.com'"
    assert_includes response.body, "INSTALL_SCRIPT_URL='https://dev.devopsellence.com/lfg.sh'"
    assert_includes response.body, 'curl -fsSL "$INSTALL_SCRIPT_URL?version=$CLI_VERSION" | bash -s -- --install-agent-skill'
    refute_includes response.body, 'curl -fsSL "$BASE_URL/lfg.sh'
  end

  test "cli install script accepts version from the query string" do
    get "/lfg.sh", params: { version: "v0.1.0-rc.1" }

    assert_response :success
    assert_includes response.body, 'CLI_VERSION="${DEVOPSELLENCE_CLI_VERSION:-}"'
    assert_includes response.body, "CLI_VERSION='v0.1.0-rc.1'"
    assert_includes response.body, "missing --version (or use ?version=... or set DEVOPSELLENCE_CLI_VERSION)"
    assert_includes response.body, 'validate_version "$CLI_VERSION"'
  end

  test "cli install script executes successfully for prerelease tags" do
    get "/lfg.sh", params: { version: "master-0053792f6aec" }

    assert_response :success

    stdout, stderr, status, installed_cli = run_cli_install_script(
      response.body,
      version: "master-0053792f6aec"
    )

    assert_predicate status, :success?, -> { "stdout:\n#{stdout}\nstderr:\n#{stderr}" }
    assert_includes stdout, "devopsellence CLI installed"
    assert_equal "prerelease build\n", installed_cli
  end

  test "cli install script can install the agent skill when requested" do
    get "/lfg.sh", params: { version: "master-0053792f6aec" }

    assert_response :success

    stdout, stderr, status, installed_cli, skill_args = run_cli_install_script(
      response.body,
      version: "master-0053792f6aec",
      install_agent_skill: true
    )

    assert_predicate status, :success?, -> { "stdout:\n#{stdout}\nstderr:\n#{stderr}" }
    assert_includes stdout, "installing devopsellence agent skill"
    assert_equal "prerelease build\n", installed_cli
    assert_equal [ "skills", "add", "devopsellence/devopsellence", "--skill", "devopsellence", "-g" ], skill_args
  end

  test "cli install script fails when requested agent skill cannot install without npx" do
    get "/lfg.sh", params: { version: "master-0053792f6aec" }

    assert_response :success

    stdout, stderr, status, installed_cli, skill_args = run_cli_install_script(
      response.body,
      version: "master-0053792f6aec",
      install_agent_skill: true,
      include_npx: false
    )

    refute_predicate status, :success?, -> { "stdout:\n#{stdout}\nstderr:\n#{stderr}" }
    assert_includes stderr, "Agent skill install requested, but npx was not found"
    assert_equal "prerelease build\n", installed_cli
    assert_nil skill_args
  end

  test "cli install script defaults to user local bin on linux" do
    get "/lfg.sh", params: { version: "master-0053792f6aec" }

    assert_response :success

    stdout, stderr, status, installed_cli = run_cli_install_script(
      response.body,
      version: "master-0053792f6aec",
      install_dir: nil
    )

    assert_predicate status, :success?, -> { "stdout:\n#{stdout}\nstderr:\n#{stderr}" }
    assert_includes stdout, "/.local/bin/devopsellence"
    assert_equal "prerelease build\n", installed_cli
  end

  test "cli install script derives checksum url after parsing base url overrides" do
    get "/lfg.sh"

    assert_response :success
    assert_includes response.body, 'CLI_CHECKSUM_URL="${DEVOPSELLENCE_CLI_CHECKSUM_URL:-}"'
    assert_includes response.body, 'CLI_CHECKSUM_URL="$BASE_URL/cli/checksums"'
    assert_includes response.body, 'ARTIFACT_NAME="cli-$OS-$ARCH"'
    assert_operator response.body.index("while [[ $# -gt 0 ]]; do"), :<, response.body.index('CLI_CHECKSUM_URL="$BASE_URL/cli/checksums"')
  end

  test "cli install script safely quotes query-string version" do
    get "/lfg.sh", params: { version: "v0.1.0-rc.1$(touch /tmp/pwned)'oops" }

    assert_response :success
    assert_includes response.body, "CLI_VERSION='v0.1.0-rc.1$(touch /tmp/pwned)'\"'\"'oops'"
    refute_includes response.body, 'CLI_VERSION="${DEVOPSELLENCE_CLI_VERSION:-v0.1.0-rc.1$(touch /tmp/pwned)\'oops}"'
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

  test "agent install script accepts version from the query string" do
    get "/install.sh", params: { version: "v0.1.0-rc.1" }

    assert_response :success
    assert_includes response.body, 'AGENT_VERSION="${DEVOPSELLENCE_AGENT_VERSION:-}"'
    assert_includes response.body, "AGENT_VERSION='v0.1.0-rc.1'"
    assert_includes response.body, 'validate_version "$AGENT_VERSION"'
    assert_includes response.body, "OS_RAW=\"$(uname -s | tr '[:upper:]' '[:lower:]')\""
    assert_includes response.body, 'ARTIFACT_NAME="agent-$OS-$ARCH"'
  end

  test "agent install script safely quotes query-string version" do
    get "/install.sh", params: { version: "v0.1.0-rc.1$(touch /tmp/pwned)'oops" }

    assert_response :success
    assert_includes response.body, "AGENT_VERSION='v0.1.0-rc.1$(touch /tmp/pwned)'\"'\"'oops'"
    refute_includes response.body, 'AGENT_VERSION="${DEVOPSELLENCE_AGENT_VERSION:-v0.1.0-rc.1$(touch /tmp/pwned)\'oops}"'
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

  private

  def run_cli_install_script(script_body, version:, install_dir: :explicit, install_agent_skill: false, include_npx: true)
    Dir.mktmpdir("devopsellence-cli-install-test") do |tmpdir|
      fixtures_dir = File.join(tmpdir, "fixtures")
      fakebin_dir = File.join(tmpdir, "fakebin")
      effective_install_dir = install_dir == :explicit ? File.join(tmpdir, "install") : install_dir
      script_path = File.join(tmpdir, "lfg.sh")
      artifact_path = File.join(fixtures_dir, "cli-linux-amd64")
      checksums_path = File.join(fixtures_dir, "cli-SHA256SUMS")
      skill_args_path = File.join(tmpdir, "skill-args")

      FileUtils.mkdir_p(fixtures_dir)
      FileUtils.mkdir_p(fakebin_dir)
      FileUtils.mkdir_p(effective_install_dir) if effective_install_dir

      File.write(artifact_path, "prerelease build\n")
      digest = Digest::SHA256.file(artifact_path).hexdigest
      File.write(checksums_path, "#{digest}  cli-linux-amd64\n")
      File.write(script_path, script_body)
      FileUtils.chmod("u+x", script_path)

      curl_path = File.join(fakebin_dir, "curl")
      File.write(curl_path, <<~SH)
        #!/usr/bin/env bash
        set -euo pipefail

        output=""
        url=""
        while [[ $# -gt 0 ]]; do
          case "$1" in
            -o)
              output="$2"
              shift 2
              ;;
            -fsSL|-f|-s|-S|-L)
              shift
              ;;
            *)
              url="$1"
              shift
              ;;
          esac
        done

        case "$url" in
          *"/cli/download?"*)
            cp #{artifact_path.inspect} "$output"
            ;;
          *"/cli/checksums?"*)
            cp #{checksums_path.inspect} "$output"
            ;;
          *)
            echo "unexpected curl url: $url" >&2
            exit 1
            ;;
        esac
      SH
      FileUtils.chmod("u+x", curl_path)

      uname_path = File.join(fakebin_dir, "uname")
      File.write(uname_path, <<~SH)
        #!/usr/bin/env bash
        set -euo pipefail

        case "${1:-}" in
          -s)
            printf 'Linux\n'
            ;;
          -m)
            printf 'x86_64\n'
            ;;
          *)
            exec /usr/bin/uname "$@"
            ;;
        esac
      SH
      FileUtils.chmod("u+x", uname_path)

      if include_npx
        npx_path = File.join(fakebin_dir, "npx")
        File.write(npx_path, <<~SH)
          #!/usr/bin/env bash
          set -euo pipefail

          printf '%s\\n' "$@" > #{skill_args_path.inspect}
        SH
        FileUtils.chmod("u+x", npx_path)
      end

      env = {
        "PATH" => "#{fakebin_dir}:#{ENV.fetch("PATH")}",
        "HOME" => tmpdir,
        "SHELL" => ENV.fetch("SHELL", "/bin/bash"),
        "DEVOPSELLENCE_CLI_VERSION" => version,
        "DEVOPSELLENCE_BASE_URL" => "https://downloads.devopsellence.test"
      }
      env["DEVOPSELLENCE_CLI_INSTALL_DIR"] = effective_install_dir if effective_install_dir
      env["DEVOPSELLENCE_INSTALL_AGENT_SKILL"] = "1" if install_agent_skill

      stdout, stderr, status = Open3.capture3(env, script_path)
      expected_install_dir = effective_install_dir || File.join(tmpdir, ".local", "bin")
      installed_cli = File.exist?(File.join(expected_install_dir, "devopsellence")) ? File.read(File.join(expected_install_dir, "devopsellence")) : nil
      skill_args = File.exist?(skill_args_path) ? File.readlines(skill_args_path, chomp: true) : nil
      [ stdout, stderr, status, installed_cli, skill_args ]
    end
  end
end
