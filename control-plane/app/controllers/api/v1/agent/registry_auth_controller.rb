# frozen_string_literal: true

module Api
  module V1
    module Agent
      class RegistryAuthController < Api::V1::Agent::BaseController
        before_action :authenticate_node_access!
        rate_limit to: 20, within: 1.minute, name: "agent_registry_auth", by: -> { request.remote_ip },
          with: -> { render_error("rate_limited", "too many requests", status: :too_many_requests) }

        def create
          image = params[:image].to_s.strip
          return render_error("invalid_request", "image is required", status: :unprocessable_entity) if image.blank?

          host = registry_host(image)
          return render_error("invalid_request", "image registry host is required", status: :unprocessable_entity) if host.blank?

          config = current_node.organization&.organization_registry_config
          return render_error("not_found", "registry auth not configured", status: :not_found) unless config
          return render_error("not_found", "registry auth not configured", status: :not_found) unless config.registry_host == host

          render json: {
            server_address: config.registry_host,
            username: config.username,
            password: config.password,
            expires_in: expires_in_for(config)
          }
        end

        private

        def registry_host(image)
          first = image.split("/", 2).first.to_s
          return "" unless first.include?(".") || first.include?(":") || first == "localhost"

          first
        end

        def expires_in_for(config)
          return 0 if config.expires_at.blank?

          [ (config.expires_at - Time.current).to_i, 0 ].max
        end
      end
    end
  end
end
