# frozen_string_literal: true

module Api
  module V1
    module Agent
      class BaseController < Api::V1::BaseController
        private

        AGENT_CAPABILITIES_HEADER = "devopsellence-agent-capabilities"

        attr_reader :current_node

        def authenticate_node_access!
          token = bearer_token
          return render_error("invalid_request", "missing bearer token", status: :unauthorized) unless token

          node = Node.find_by_access_token(token)
          return render_error("invalid_grant", "invalid access_token", status: :unauthorized) unless node
          return render_error("invalid_grant", "access_token expired", status: :unauthorized) unless node.access_active?

          node.touch_last_seen_at_if_stale!
          persist_agent_capabilities!(node)
          @current_node = node
        end

        def bearer_token
          scheme, value = request.authorization.to_s.split(" ", 2)
          return nil unless scheme&.casecmp("Bearer")&.zero?

          value.to_s.presence
        end

        def current_agent_capabilities
          @current_agent_capabilities ||= request.headers.fetch(AGENT_CAPABILITIES_HEADER, "").to_s.split(",").filter_map do |entry|
            capability = entry.to_s.strip
            capability.presence
          end.uniq.sort
        end

        def persist_agent_capabilities!(node)
          return if node.capabilities == current_agent_capabilities

          node.update_column(:capabilities_json, JSON.generate(current_agent_capabilities))
        end
      end
    end
  end
end
