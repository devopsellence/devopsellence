# frozen_string_literal: true

module Nodes
  class Cleanup
    Result = Struct.new(:node, :environment, :desired_state, keyword_init: true)

    def initialize(node:, store: Storage::ObjectStore.build, revoked_at: Time.current, broker: nil, logger: Rails.logger)
      @node = node
      @store = store
      @revoked_at = revoked_at
      @broker = broker || Runtime::Broker.current
      @logger = logger
    end

    def call
      environment = nil
      node_bundle = nil

      Node.transaction do
        node.lock!
        environment = node.environment
        node_bundle = node.node_bundle
        node.update!(environment: nil)

        node.update!(
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
      schedule_managed_server_delete if node.managed?
      Runtime::EnsureBundles.enqueue if node_bundle
      EnvironmentIngresses::ReconcileJob.perform_later(environment.id) if environment

      Result.new(node:, environment:, desired_state: nil)
    end

    private

    attr_reader :node, :store, :revoked_at, :broker, :logger

    def schedule_managed_server_delete
      ManagedNodes::DeleteJob.perform_later(node_id: node.id)
    end

    def revoke_bundle_impersonation!(bundle)
      broker.revoke_node_bundle_impersonation!(bundle:)
    rescue StandardError => error
      logger.warn("[nodes/cleanup] bundle impersonation revocation failed: #{error.message}")
    end
  end
end
