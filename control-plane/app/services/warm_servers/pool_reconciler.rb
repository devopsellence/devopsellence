# frozen_string_literal: true

module WarmServers
  class PoolReconciler
    def initialize(runtime: Devopsellence::RuntimeConfig.current, provisioner_class: Provisioner, provider_resolver: nil)
      @runtime = runtime
      @provisioner_class = provisioner_class
      @provider_resolver = provider_resolver || ->(slug) { ManagedNodes::Providers::Resolver.resolve(slug) }
    end

    def call
      target = runtime.managed_pool_target.to_i
      return if target <= 0

      cleanup_stale_bootstraps!

      current_warm = warm_node_count + active_bootstrap_count

      deficit = target - current_warm
      deficit.times { provisioner_for_pool_refill.call } if deficit.positive?
    end

    private

    attr_reader :runtime, :provisioner_class, :provider_resolver

    def provisioner_for_pool_refill
      return provisioner_class.new(wait_for_registration: false) if provisioner_class == Provisioner

      provisioner_class.new
    end

    def warm_node_count
      Node.where(
        managed: true,
        organization_id: nil,
        environment_id: nil,
        node_bundle_id: nil,
        revoked_at: nil,
        provisioning_status: Node::PROVISIONING_READY
      ).where(lease_expires_at: nil).count
    end

    def active_bootstrap_count
      active_bootstraps.count
    end

    def active_bootstraps
      NodeBootstrapToken.where(
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
        node_id: nil,
        consumed_at: nil
      ).where(created_at: bootstrap_cutoff..).where.not(provider_server_id: [ nil, "" ])
    end

    def cleanup_stale_bootstraps!
      stale_bootstraps.find_each do |bootstrap_token|
        begin
          if bootstrap_token.provider_server_id.present?
            provider_resolver.call(bootstrap_token.managed_provider).delete_server(
              provider_server_id: bootstrap_token.provider_server_id
            )
          end
          bootstrap_token.update!(consumed_at: Time.current)
          Rails.logger.info("[warm_servers/pool_reconciler] cleaned stale bootstrap token=#{bootstrap_token.id} provider=#{bootstrap_token.managed_provider} server_id=#{bootstrap_token.provider_server_id}")
        rescue StandardError => error
          Rails.logger.warn("[warm_servers/pool_reconciler] failed cleaning stale bootstrap token=#{bootstrap_token.id} error=#{error.message}")
        end
      end
    end

    def stale_bootstraps
      NodeBootstrapToken.where(
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
        node_id: nil,
        consumed_at: nil
      ).where(created_at: ...bootstrap_cutoff)
    end

    def bootstrap_cutoff
      runtime.managed_registration_timeout_seconds.to_i.seconds.ago
    end
  end
end
