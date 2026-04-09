# frozen_string_literal: true

module ManagedNodes
  class RetireNode
    def initialize(node:, revoked_at: Time.current, delete_server_class: ManagedNodes::DeleteServer, broker: nil, logger: Rails.logger)
      @node = node
      @revoked_at = revoked_at
      @delete_server_class = delete_server_class
      @broker = broker || Runtime::Broker.current
      @logger = logger
    end

    def call
      node_bundle = node.node_bundle
      logger.info("[retire_node] retiring node node_id=#{node.id} name=#{node.name} managed=#{node.managed?} bundle=#{node_bundle&.token || "none"}")
      revoke_node_tokens!
      if node.managed?
        logger.info("[retire_node] deleting server node_id=#{node.id} provider_server_id=#{node.provider_server_id}")
        delete_server_class.new(node: node).call
      end
      revoke_bundle_impersonation!(node_bundle) if node_bundle
      node_bundle&.destroy!
      node.destroy!
      logger.info("[retire_node] node retired node_id=#{node.id}")
      Runtime::EnsureBundles.enqueue if node_bundle
    end

    private

    attr_reader :node, :revoked_at, :delete_server_class, :broker, :logger

    def revoke_bundle_impersonation!(bundle)
      broker.revoke_node_bundle_impersonation!(bundle:)
    rescue StandardError => error
      logger.warn("[retire_node] bundle impersonation revocation failed: #{error.message}")
    end

    def revoke_node_tokens!
      Node.transaction do
        node.lock!
        node.update!(
          environment: nil,
          organization: nil,
          node_bundle: nil,
          revoked_at: revoked_at,
          access_expires_at: revoked_at,
          refresh_expires_at: revoked_at,
          lease_expires_at: nil
        )
      end
    end
  end
end
