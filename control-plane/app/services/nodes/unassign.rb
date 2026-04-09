# frozen_string_literal: true

module Nodes
  class Unassign
    Result = Struct.new(:node, :environment, :node_bundle, keyword_init: true)

    def initialize(node:, broker: nil, logger: Rails.logger)
      @node = node
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
        node.update!(
          environment: nil,
          node_bundle: nil,
          desired_state_bucket: "",
          desired_state_object_path: "",
          lease_expires_at: nil
        )
      end

      revoke_bundle_impersonation!(node_bundle) if node_bundle
      node_bundle&.destroy!
      Runtime::EnsureBundles.enqueue if node_bundle
      EnvironmentIngresses::ReconcileJob.perform_later(environment.id) if environment

      Result.new(node:, environment:, node_bundle:)
    end

    private

    attr_reader :node, :broker, :logger

    def revoke_bundle_impersonation!(bundle)
      broker.revoke_node_bundle_impersonation!(bundle:)
    rescue StandardError => error
      logger.warn("[nodes/unassign] bundle impersonation revocation failed: #{error.message}")
    end
  end
end
