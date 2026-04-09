# frozen_string_literal: true

require "test_helper"

module WarmServers
  class ProvisionerTest < ActiveSupport::TestCase
    FakeProvider = Struct.new(:deleted_provider_ids) do
      def delete_server(provider_server_id:)
        deleted_provider_ids << provider_server_id
      end
    end

    class FakeManagedProvisioner
      def initialize(result)
        @result = result
      end

      def generate_node_name(prefix:)
        "#{prefix}-test"
      end

      def call(node_name:)
        @result
      end
    end

    test "deletes provider server and consumes bootstrap token when registration fails" do
      bootstrap_token, = NodeBootstrapToken.issue!(
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11"
      )
      bootstrap_token.update!(provider_server_id: "server-1", public_ip: "198.51.100.10")

      provision_result = ManagedNodes::Provisioner::Result.new(
        bootstrap_token:,
        raw_bootstrap_token: "raw-token",
        server: Struct.new(:id).new("server-1"),
        node_name: "devopsellence-warm-test"
      )
      provider = FakeProvider.new([])

      error = assert_raises(Provisioner::Error) do
        Provisioner.new(
          managed_provisioner: FakeManagedProvisioner.new(provision_result),
          registration_waiter: ->(_token) { raise ManagedNodes::WaitForRegistration::Error, "managed node registration timed out after 180s" },
          provider_resolver: ->(_slug) { provider }
        ).call
      end

      assert_equal "managed node registration timed out after 180s", error.message
      assert_equal [ "server-1" ], provider.deleted_provider_ids
      assert bootstrap_token.reload.consumed_at.present?
    end

    test "reports registration wait progress" do
      bootstrap_token, = NodeBootstrapToken.issue!(
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11"
      )
      provision_result = ManagedNodes::Provisioner::Result.new(
        bootstrap_token:,
        raw_bootstrap_token: "raw-token",
        server: Struct.new(:id).new("server-1"),
        node_name: "devopsellence-warm-test"
      )
      progress = []
      node = Node.create!(
        name: "devopsellence-warm-test",
        access_token_digest: Node.digest(SecureRandom.hex(Node::TOKEN_BYTES)),
        refresh_token_digest: Node.digest(SecureRandom.hex(Node::TOKEN_BYTES)),
        access_expires_at: Node::ACCESS_TTL.from_now,
        refresh_expires_at: Node::REFRESH_TTL.from_now,
        labels_json: [].to_json,
        desired_state_bucket: "",
        desired_state_object_path: ""
      )

      Provisioner.new(
        managed_provisioner: FakeManagedProvisioner.new(provision_result),
        registration_waiter: ->(_token) { node },
        provider_resolver: ->(_slug) { FakeProvider.new([]) },
        on_progress: ->(message) { progress << message }
      ).call

      assert_equal [ "waiting for managed node registration" ], progress
    end

    test "can leave registration pending for background warm-pool refill" do
      bootstrap_token, = NodeBootstrapToken.issue!(
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11"
      )
      provision_result = ManagedNodes::Provisioner::Result.new(
        bootstrap_token:,
        raw_bootstrap_token: "raw-token",
        server: Struct.new(:id).new("server-1"),
        node_name: "devopsellence-warm-test"
      )

      result = Provisioner.new(
        managed_provisioner: FakeManagedProvisioner.new(provision_result),
        registration_waiter: ->(_token) { raise "should not wait" },
        provider_resolver: ->(_slug) { FakeProvider.new([]) },
        wait_for_registration: false
      ).call

      assert_equal bootstrap_token, result
      assert_nil bootstrap_token.reload.node_id
      assert_nil bootstrap_token.consumed_at
    end
  end
end
