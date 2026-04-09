# frozen_string_literal: true

module WarmServers
  class Claim
    Error = Class.new(StandardError)
    RESERVATION_TTL = 10.minutes
    EXISTING_PROVISIONING_WAIT_TIMEOUT = 45.seconds
    EXISTING_PROVISIONING_POLL_INTERVAL = 1.second

    def initialize(provisioner_class: Provisioner, on_progress: nil, clock: nil,
                   existing_provisioning_wait_timeout: EXISTING_PROVISIONING_WAIT_TIMEOUT,
                   existing_provisioning_poll_interval: EXISTING_PROVISIONING_POLL_INTERVAL,
                   sleeper: nil)
      @provisioner_class = provisioner_class
      @on_progress = on_progress
      @clock = clock || -> { Time.current }
      @existing_provisioning_wait_timeout = existing_provisioning_wait_timeout
      @existing_provisioning_poll_interval = existing_provisioning_poll_interval
      @sleeper = sleeper || ->(duration) { sleep(duration) }
    end

    def call
      claim_existing_warm_server || claim_provisioning_server || claim_provisioned_server
    end

    private

    attr_reader :provisioner_class, :on_progress, :clock, :existing_provisioning_wait_timeout,
      :existing_provisioning_poll_interval, :sleeper

    def claim_existing_warm_server
      Node.transaction do
        node = warm_server_scope.order(:created_at).lock("FOR UPDATE SKIP LOCKED").first
        next unless node

        reserve_node!(node)
      end
    end

    def claim_provisioning_server
      node = wait_for_provisioning_server
      return if node.blank?

      node.with_lock do
        return unless node.warm_pool_candidate?

        reserve_node!(node)
      end
    end

    def claim_provisioned_server
      node = provision_new_server
      node.with_lock do
        raise Error, "server is no longer available" unless node.warm_pool_candidate?

        reserve_node!(node)
      end
    end

    def reserve_node!(node)
      # Reserve node immediately to prevent concurrent claims from picking the same server.
      # lease_expires_at is overwritten by NodeBundles::Claim#associate_node! on successful assignment.
      node.update!(lease_expires_at: Time.current + RESERVATION_TTL)
      node
    end

    def warm_server_scope
      Node.where(
        managed: true,
        organization_id: nil,
        environment_id: nil,
        node_bundle_id: nil,
        revoked_at: nil,
        provisioning_status: Node::PROVISIONING_READY,
        lease_expires_at: nil
      )
    end

    def wait_for_provisioning_server
      deadline = clock.call + existing_provisioning_wait_timeout

      loop do
        node = next_existing_warm_server
        if node.present?
          return node
        end

        bootstrap_token = provisioning_bootstrap_token
        if bootstrap_token.blank?
          return nil
        end

        bootstrap_token.reload
        if bootstrap_token.node.present?
          return bootstrap_token.node
        end
        if !bootstrap_token.active?
          return nil
        end
        if clock.call >= deadline
          return nil
        end

        sleeper.call(existing_provisioning_poll_interval)
      end
    end

    def next_existing_warm_server
      Node.transaction do
        warm_server_scope.order(:created_at).lock("FOR UPDATE SKIP LOCKED").first
      end
    end

    def provisioning_bootstrap_token
      NodeBootstrapToken.where(
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE
      ).where(created_at: bootstrap_cutoff..).where.not(provider_server_id: [ nil, "" ])
        .where("node_id IS NOT NULL OR consumed_at IS NULL")
        .order(:created_at)
        .first
    end

    def bootstrap_cutoff
      Devopsellence::RuntimeConfig.current.managed_registration_timeout_seconds.to_i.seconds.ago
    end

    def provision_new_server
      on_progress&.call("provisioning warm server")
      provisioner_class.new(on_progress:).call
    end
  end
end
