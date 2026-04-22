# frozen_string_literal: true

require "test_helper"

class AgentInstallScriptTest < ActiveSupport::TestCase
  test "render safely embeds default base url without shell evaluation" do
    script = AgentInstallScript.render(
      base_url: "https://example.com$(touch /tmp/pwned)",
      default_version: "v1.2.3"
    )

    assert_includes script, 'BASE_URL="${DEVOPSELLENCE_BASE_URL:-}"'
    assert_includes script, "BASE_URL='https://example.com$(touch /tmp/pwned)'"
    refute_includes script, 'BASE_URL="${DEVOPSELLENCE_BASE_URL:-https://example.com$(touch /tmp/pwned)}"'
  end

  test "render safely embeds default agent version without shell evaluation" do
    script = AgentInstallScript.render(
      base_url: "https://example.com",
      default_version: "v1.0.0$(rm -rf /)"
    )

    assert_includes script, 'AGENT_VERSION="${DEVOPSELLENCE_AGENT_VERSION:-}"'
    assert_includes script, "AGENT_VERSION='v1.0.0$(rm -rf /)'"
    refute_includes script, 'AGENT_VERSION="${DEVOPSELLENCE_AGENT_VERSION:-v1.0.0$(rm -rf /)}"'
  end

  test "render accepts prerelease-style agent tags" do
    script = AgentInstallScript.render(
      base_url: "https://example.com",
      default_version: "master-0053792f6aec"
    )

    assert_includes script, "AGENT_VERSION='master-0053792f6aec'"
    assert_includes script, 'if [[ ! "$version" =~ ^[0-9A-Za-z][0-9A-Za-z._-]*$ ]]; then'
  end
end
