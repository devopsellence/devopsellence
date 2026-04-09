# frozen_string_literal: true

require "test_helper"

class ManagedNodesBootstrapScriptTest < ActiveSupport::TestCase
  test "embeds install script in cloud-init runcmd" do
    script = ManagedNodes::BootstrapScript.new(
      node_name: "node-a",
      bootstrap_token: "token-123",
      base_url: "https://dev.devopsellence.com",
      agent_version: "v0.0.0-dev"
    ).render

    assert_includes script, "bash -s -- --token token-123 --base-url https://dev.devopsellence.com --agent-version v0.0.0-dev <<'DEVOPSELLENCE_INSTALL'"
    assert_includes script, "\n            #!/usr/bin/env bash\n"
    assert_includes script, "set -euo pipefail"
    assert_includes script, "DOWNLOAD_URL=\"$AGENT_URL?os=$OS&arch=$ARCH\""
    assert_includes script, "path: /etc/ssh/sshd_config.d/60-devopsellence-hardening.conf"
    assert_includes script, "PasswordAuthentication no"
    assert_includes script, "KbdInteractiveAuthentication no"
    assert_includes script, "ChallengeResponseAuthentication no"
    assert_includes script, "PermitRootLogin prohibit-password"
    assert_includes script, "apt-get install -y ufw"
    assert_includes script, "ufw default deny incoming"
    assert_includes script, "ufw allow 22/tcp"
    assert_includes script, "ufw --force enable"
    assert_includes script, "systemctl reload ssh || systemctl reload sshd || true"
    refute_includes script, "/install.sh"
  end
end
