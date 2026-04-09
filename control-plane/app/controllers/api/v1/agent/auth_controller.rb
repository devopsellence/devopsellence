# frozen_string_literal: true

module Api
  module V1
    module Agent
      class AuthController < Api::V1::Agent::BaseController
        def refresh
          refresh_token = params[:refresh_token].to_s
          return render_error("invalid_request", "missing refresh_token") if refresh_token.blank?

          node = Node.find_by_refresh_token(refresh_token)
          return render_error("invalid_grant", "invalid refresh_token", status: :unauthorized) unless node

          raw_access = nil
          raw_refresh = nil

          node.with_lock do
            return render_error("invalid_grant", "refresh_token expired", status: :unauthorized) unless node.refresh_active?

            unless node.refresh_token_digest == Node.digest(refresh_token)
              return render_error("invalid_grant", "refresh_token reused", status: :unauthorized)
            end

            raw_access, raw_refresh = node.rotate_tokens!
          end
          persist_agent_capabilities!(node)

          render json: {
            access_token: raw_access,
            refresh_token: raw_refresh,
            token_type: "Bearer",
            expires_in: (node.access_expires_at - Time.current).to_i,
            desired_state_target: Nodes::DesiredStateTarget.for(node:, capabilities: current_agent_capabilities)
          }
        end
      end
    end
  end
end
