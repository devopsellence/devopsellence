# frozen_string_literal: true

require "test_helper"

module WarmServers
  class PoolReconcilerTest < ActiveSupport::TestCase
    FakeProvider = Struct.new(:deleted_provider_ids) do
      def delete_server(provider_server_id:)
        deleted_provider_ids << provider_server_id
      end
    end

    class FakeProvisioner
      class << self
        attr_accessor :calls
      end

      def call
        self.class.calls += 1
      end
    end

    setup do
      FakeProvisioner.calls = 0
    end

    test "cleans stale managed bootstrap tokens before refilling warm capacity" do
      bootstrap_token, = NodeBootstrapToken.issue!(
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11"
      )
      bootstrap_token.update!(provider_server_id: "server-1", public_ip: "198.51.100.10")
      bootstrap_token.update_columns(created_at: 10.minutes.ago, updated_at: 10.minutes.ago)
      provider = FakeProvider.new([])

      with_runtime_config(managed_pool_target: 1, managed_registration_timeout_seconds: 60) do
        PoolReconciler.new(
          provisioner_class: FakeProvisioner,
          provider_resolver: ->(_slug) { provider }
        ).call
      end

      assert_includes provider.deleted_provider_ids, "server-1"
      assert bootstrap_token.reload.consumed_at.present?
      assert_equal 1, FakeProvisioner.calls
    end

    test "counts active bootstrap tokens as in-flight warm capacity" do
      bootstrap_token, = NodeBootstrapToken.issue!(
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11"
      )
      bootstrap_token.update!(provider_server_id: "server-1", public_ip: "198.51.100.10")

      with_runtime_config(managed_pool_target: 1, managed_registration_timeout_seconds: 60) do
        PoolReconciler.new(provisioner_class: FakeProvisioner).call
      end

      assert_equal 0, FakeProvisioner.calls
      assert_nil bootstrap_token.reload.consumed_at
    end
  end
end
