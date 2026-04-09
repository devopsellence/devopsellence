# frozen_string_literal: true

require "test_helper"
require "thread"

module WarmServers
  class ClaimTest < ActiveSupport::TestCase
    self.use_transactional_tests = false

    setup do
      NodeBootstrapToken.delete_all
      Node.delete_all
      Node.where(
        managed: true,
        organization_id: nil,
        environment_id: nil,
        node_bundle_id: nil,
        revoked_at: nil,
        provisioning_status: Node::PROVISIONING_READY,
        lease_expires_at: nil
      ).update_all(revoked_at: Time.current, updated_at: Time.current)
    end

    teardown do
      NodeBootstrapToken.delete_all
      Node.delete_all
    end

    test "skips locked warm servers and reserves the next available node" do
      first, = issue_test_node!(
        organization: nil,
        name: "warm-1",
        managed: true,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11",
        provider_server_id: "srv-1"
      )
      first.update_column(:created_at, Time.utc(2000, 1, 1))
      second, = issue_test_node!(
        organization: nil,
        name: "warm-2",
        managed: true,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11",
        provider_server_id: "srv-2"
      )
      second.update_column(:created_at, Time.utc(2000, 1, 2))

      ready = Queue.new
      release = Queue.new

      locker = Thread.new do
        ActiveRecord::Base.connection_pool.with_connection do
          Node.transaction do
            Node.lock.find(first.id)
            ready << true
            release.pop
          end
        end
      end

      ready.pop
      claimed = WarmServers::Claim.new.call

      assert_equal second.id, claimed.id
      assert claimed.reload.lease_expires_at.present?
      assert_nil first.reload.lease_expires_at
    ensure
      release << true if release
      locker&.join
    end

    test "provisions a new warm server when all existing candidates are locked" do
      locked_node, = issue_test_node!(
        organization: nil,
        name: "warm-locked",
        managed: true,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11",
        provider_server_id: "srv-locked"
      )
      locked_node.update_column(:created_at, Time.utc(2000, 1, 1))

      ready = Queue.new
      release = Queue.new

      locker = Thread.new do
        ActiveRecord::Base.connection_pool.with_connection do
          Node.transaction do
            Node.lock.find(locked_node.id)
            ready << true
            release.pop
          end
        end
      end

      provisioner_class = Class.new do
        def initialize(on_progress: nil)
        end

        def call
          Node.create!(
            organization: nil,
            name: "warm-provisioned",
            access_token_digest: Node.digest(SecureRandom.hex(Node::TOKEN_BYTES)),
            refresh_token_digest: Node.digest(SecureRandom.hex(Node::TOKEN_BYTES)),
            access_expires_at: Node::ACCESS_TTL.from_now,
            refresh_expires_at: Node::REFRESH_TTL.from_now,
            provisioning_status: Node::PROVISIONING_READY,
            provisioning_error: nil,
            managed: true,
            managed_provider: "hetzner",
            managed_region: "ash",
            managed_size_slug: "cpx11",
            provider_server_id: "srv-provisioned",
            labels_json: [].to_json,
            desired_state_bucket: "",
            desired_state_object_path: ""
          )
        end
      end

      ready.pop
      claimed = WarmServers::Claim.new(provisioner_class: provisioner_class).call

      assert_equal "warm-provisioned", claimed.name
      assert claimed.reload.lease_expires_at.present?
      assert_nil locked_node.reload.lease_expires_at
    ensure
      release << true if release
      locker&.join
    end

    test "reports progress when provisioning a new warm server" do
      progress = []

      provisioner_class = Class.new do
        def initialize(on_progress: nil)
          @on_progress = on_progress
        end

        def call
          @on_progress&.call("waiting for managed node registration")
          Node.create!(
            organization: nil,
            name: "warm-provisioned",
            access_token_digest: Node.digest(SecureRandom.hex(Node::TOKEN_BYTES)),
            refresh_token_digest: Node.digest(SecureRandom.hex(Node::TOKEN_BYTES)),
            access_expires_at: Node::ACCESS_TTL.from_now,
            refresh_expires_at: Node::REFRESH_TTL.from_now,
            provisioning_status: Node::PROVISIONING_READY,
            provisioning_error: nil,
            managed: true,
            managed_provider: "hetzner",
            managed_region: "ash",
            managed_size_slug: "cpx11",
            provider_server_id: "srv-provisioned",
            labels_json: [].to_json,
            desired_state_bucket: "",
            desired_state_object_path: ""
          )
        end
      end

      WarmServers::Claim.new(
        provisioner_class: provisioner_class,
        on_progress: ->(message) { progress << message }
      ).call

      assert_equal [ "provisioning warm server", "waiting for managed node registration" ], progress
    end

    test "reuses an in-flight managed bootstrap before provisioning another server" do
      bootstrap_token, = NodeBootstrapToken.issue!(
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
        managed_provider: "hetzner",
        managed_region: "ash",
        managed_size_slug: "cpx11"
      )
      bootstrap_token.update!(provider_server_id: "srv-bootstrap", public_ip: "198.51.100.10")

      provisioner_class = stub("provisioner_class")
      provisioner_class.expects(:new).never
      sleeper = ->(_duration) do
        node, = issue_test_node!(
          organization: nil,
          name: "warm-from-bootstrap",
          managed: true,
          managed_provider: "hetzner",
          managed_region: "ash",
          managed_size_slug: "cpx11",
          provider_server_id: "srv-bootstrap"
        )
        bootstrap_token.update!(node:, consumed_at: Time.current)
      end

      claimed = WarmServers::Claim.new(
        provisioner_class: provisioner_class,
        existing_provisioning_wait_timeout: 5.seconds,
        existing_provisioning_poll_interval: 1.second,
        sleeper: sleeper
      ).call

      assert_equal "warm-from-bootstrap", claimed.name
      assert claimed.reload.lease_expires_at.present?
    end
  end
end
