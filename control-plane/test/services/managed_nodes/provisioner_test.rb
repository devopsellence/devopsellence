# frozen_string_literal: true

require "test_helper"

module ManagedNodes
  class ProvisionerTest < ActiveSupport::TestCase
    FakeServer = Struct.new(:id, :status, :public_ip, keyword_init: true)

    class FlakyProvider
      attr_reader :attempts

      def initialize(failures:)
        @failures = failures
        @attempts = 0
      end

      def create_server(**)
        @attempts += 1
        raise placement_error if @attempts <= @failures

        FakeServer.new(id: "server-#{@attempts}", status: "running", public_ip: "198.51.100.10")
      end

      def public_ip(server)
        server.public_ip
      end

      def delete_server(provider_server_id:)
      end

      private

      def placement_error
        RuntimeError.new(<<~MSG.strip)
          hetzner server create failed (412): {
           "error": {
            "code": "resource_unavailable",
            "message": "error during placement",
            "details": {}
           }
          }
        MSG
      end
    end

    test "falls through to the next configured pool after placement failure" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      primary = FlakyProvider.new(failures: 3)
      secondary = FlakyProvider.new(failures: 0)
      sleeps = []

      provider_resolver = lambda do |slug|
        case slug
        when "hetzner"
          secondary
        else
          raise "unexpected provider #{slug}"
        end
      end

      result = Provisioner.new(
        organization: organization,
        provider_slug: "hetzner",
        region: "ash",
        size_slug: "cpx11",
        pool_candidates: [
          { provider_slug: "hetzner", region: "ash", size_slug: "cpx11" },
          { provider_slug: "hetzner", region: "hil", size_slug: "cpx11" }
        ],
        base_url: "https://dev.test.devopsellence.com",
        provider: primary,
        provider_resolver: provider_resolver,
        sleeper: ->(seconds) { sleeps << seconds }
      ).call(node_name: "pool-node")

      assert_equal 3, primary.attempts
      assert_equal 1, secondary.attempts
      assert_equal [5, 15], sleeps
      assert_equal "server-1", result.server.id
      assert_equal "hil", result.bootstrap_token.reload.managed_region
    end

    test "retries transient placement errors before succeeding" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      provider = FlakyProvider.new(failures: 2)
      sleeps = []

      result = Provisioner.new(
        organization: organization,
        provider_slug: "hetzner",
        region: "ash",
        size_slug: "cpx11",
        base_url: "https://dev.test.devopsellence.com",
        provider: provider,
        sleeper: ->(seconds) { sleeps << seconds }
      ).call(node_name: "pool-node")

      assert_equal 3, provider.attempts
      assert_equal [5, 15], sleeps
      assert_equal "server-3", result.server.id
      assert_equal "198.51.100.10", result.bootstrap_token.reload.public_ip
      assert_equal "server-3", result.bootstrap_token.provider_server_id
    end

    test "returns friendly message when placement stays unavailable" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      provider = FlakyProvider.new(failures: 3)

      error = assert_raises(Provisioner::Error) do
        Provisioner.new(
          organization: organization,
          provider_slug: "hetzner",
          region: "ash",
          size_slug: "cpx11",
          base_url: "https://dev.test.devopsellence.com",
          provider: provider,
          sleeper: ->(_seconds) { }
        ).call(node_name: "pool-node")
      end

      assert_equal "No managed server capacity is available in ash/cpx11 right now. Retry in a few minutes, or use your own VM/server with `devopsellence init --mode solo`.", error.message
      assert_equal 3, provider.attempts
      assert NodeBootstrapToken.order(:id).last.consumed_at.present?
    end

    test "friendly placement error lists every candidate pool" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      primary = FlakyProvider.new(failures: 3)
      secondary = FlakyProvider.new(failures: 3)
      provider_resolver = ->(_slug) { secondary }

      error = assert_raises(Provisioner::Error) do
        Provisioner.new(
          organization: organization,
          provider_slug: "hetzner",
          region: "ash",
          size_slug: "cpx11",
          pool_candidates: [
            { provider_slug: "hetzner", region: "ash", size_slug: "cpx11" },
            { provider_slug: "hetzner", region: "hil", size_slug: "cpx11" }
          ],
          base_url: "https://dev.test.devopsellence.com",
          provider: primary,
          provider_resolver: provider_resolver,
          sleeper: ->(_seconds) { }
        ).call(node_name: "pool-node")
      end

      assert_equal "No managed server capacity is available in ash/cpx11, hil/cpx11 right now. Retry in a few minutes, or use your own VM/server with `devopsellence init --mode solo`.", error.message
      assert_equal 3, primary.attempts
      assert_equal 3, secondary.attempts
    end
  end
end
