# frozen_string_literal: true

module Nodes
  class Retire
    Result = Struct.new(:node, :revoked_at, keyword_init: true)

    def initialize(node:, revoked_at: Time.current, broker: nil, logger: Rails.logger)
      @node = node
      @revoked_at = revoked_at
      @broker = broker
      @logger = logger
    end

    def call
      node_bundle = nil

      Node.transaction do
        node.lock!
        node_bundle = node.node_bundle
        node.update!(
          environment: nil,
          organization: nil,
          node_bundle: nil,
          desired_state_bucket: "",
          desired_state_object_path: "",
          lease_expires_at: nil,
          revoked_at: revoked_at,
          access_expires_at: revoked_at,
          refresh_expires_at: revoked_at
        )
      end

      revoke_bundle_impersonation!(node_bundle) if node_bundle
      node_bundle&.destroy!
      node.destroy!
      Runtime::EnsureBundles.enqueue if node_bundle

      Result.new(node:, revoked_at:)
    end

    private

    attr_reader :node, :revoked_at, :logger

    def broker
      @broker ||= Runtime::Broker.current
    end

    def revoke_bundle_impersonation!(bundle)
      broker.revoke_node_bundle_impersonation!(bundle:)
    rescue StandardError => error
      logger.warn("[nodes/retire] bundle impersonation revocation failed: #{error.message}")
    end
  end
end
