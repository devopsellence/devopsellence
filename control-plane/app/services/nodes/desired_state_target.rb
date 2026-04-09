# frozen_string_literal: true

module Nodes
  class DesiredStateTarget
    class << self
      def for(node:, capabilities: [])
        bundle = node.node_bundle
        return nil unless bundle
        return nil if node.desired_state_sequence.to_i <= 0
        return nil if node.desired_state_uri.blank?

        {
          mode: "assigned",
          desired_state_sequence: node.desired_state_sequence,
          desired_state_uri: desired_state_uri_for(node:, capabilities:),
          organization_bundle_token: bundle.organization_bundle.token,
          environment_bundle_token: bundle.environment_bundle.token,
          node_bundle_token: bundle.token
        }
      end

      private

      def desired_state_uri_for(node:, capabilities:)
        return node.desired_state_uri unless pointer_capable?(capabilities)

        Nodes::DesiredStatePointer.pointer_uri(
          bucket: node.desired_state_bucket,
          reference_path: node.desired_state_object_path
        ) || node.desired_state_uri
      end

      def pointer_capable?(capabilities)
        Array(capabilities).map { |entry| entry.to_s.strip }.include?(Nodes::DesiredStatePointer::CAPABILITY)
      end
    end
  end
end
