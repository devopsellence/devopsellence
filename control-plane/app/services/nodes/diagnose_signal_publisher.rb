# frozen_string_literal: true

module Nodes
  class DiagnoseSignalPublisher
    def initialize(node:, logger: Rails.logger)
      @node = node
      @logger = logger
    end

    def call
      return false unless publishable?

      DesiredStatePublisher.new(node: node).call
      true
    rescue StandardError => error
      logger.warn("[nodes/diagnose_signal_publisher] node_id=#{node.id} republish failed: #{error.class}: #{error.message}")
      false
    end

    private
      attr_reader :node, :logger

      def publishable?
        node.environment.present? &&
          node.desired_state_bucket.to_s.strip.present? &&
          node.desired_state_object_path.to_s.strip.present?
      end
  end
end
