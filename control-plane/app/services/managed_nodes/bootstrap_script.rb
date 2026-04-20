# frozen_string_literal: true

require "shellwords"

module ManagedNodes
  class BootstrapScript
    HEREDOC_MARKER = "DEVOPSELLENCE_INSTALL".freeze
    SSH_HARDENING_CONFIG_PATH = "/etc/ssh/sshd_config.d/60-devopsellence-hardening.conf".freeze

    def initialize(node_name:, bootstrap_token:, base_url:, agent_version: Devopsellence::RuntimeConfig.current.stable_version)
      @node_name = node_name.to_s.strip
      @bootstrap_token = bootstrap_token.to_s
      @base_url = base_url.to_s.sub(%r{/*$}, "")
      @agent_version = agent_version.to_s.strip
    end

    def render
      <<~YAML
        #cloud-config
        preserve_hostname: false
        hostname: #{node_name}
        fqdn: #{node_name}
        write_files:
          - path: #{SSH_HARDENING_CONFIG_PATH}
            permissions: "0644"
            content: |
              PasswordAuthentication no
              KbdInteractiveAuthentication no
              ChallengeResponseAuthentication no
              PubkeyAuthentication yes
              PermitRootLogin prohibit-password
        runcmd:
          - export DEBIAN_FRONTEND=noninteractive
          - apt-get update
          - apt-get install -y ufw
          - ufw --force reset
          - ufw default deny incoming
          - ufw default allow outgoing
          - ufw allow 22/tcp
          - ufw --force enable
          - systemctl reload ssh || systemctl reload sshd || true
          - |
            bash -s -- --token #{Shellwords.escape(bootstrap_token)} --base-url #{Shellwords.escape(base_url)}#{agent_version_argument} <<'#{HEREDOC_MARKER}'
#{indented_install_script}
            #{HEREDOC_MARKER}
      YAML
    end

    private

    attr_reader :node_name, :bootstrap_token, :base_url, :agent_version

    def install_script
      AgentInstallScript.render(base_url: base_url, default_version: agent_version)
    end

    def indented_install_script
      install_script.lines.map { |line| "            #{line}" }.join.chomp
    end

    def agent_version_argument
      return "" unless agent_version.present?

      " --agent-version #{Shellwords.escape(agent_version)}"
    end
  end
end
