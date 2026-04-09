# frozen_string_literal: true

require "test_helper"

module ManagedNodes
  class WaitForRegistrationTest < ActiveSupport::TestCase
    test "hydrates registered node from bootstrap token metadata without raw token access" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)

      bootstrap_token, = NodeBootstrapToken.issue!(
        organization: organization,
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11"
      )

      node, = issue_test_node!(organization: organization, name: "warm-node")
      bootstrap_token.update!(
        node: node,
        provider_server_id: "srv-123",
        public_ip: "198.51.100.20"
      )

      hydrated_node = WaitForRegistration.new(
        bootstrap_token: bootstrap_token,
        issuer: "https://dev.test.devopsellence.com",
        timeout_seconds: 1,
        poll_interval_seconds: 0
      ).call

      assert_equal node.id, hydrated_node.id
      assert_predicate hydrated_node, :managed?
      assert_equal "hetzner", hydrated_node.managed_provider
      assert_equal "ash", hydrated_node.managed_region
      assert_equal "cpx11", hydrated_node.managed_size_slug
      assert_equal "srv-123", hydrated_node.provider_server_id
      assert_equal "198.51.100.20", hydrated_node.public_ip
      assert_equal Node::PROVISIONING_READY, hydrated_node.provisioning_status
    end
  end
end
