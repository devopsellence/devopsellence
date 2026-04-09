# frozen_string_literal: true

module Api
  module V1
    module Agent
      class SecretsController < Api::V1::Agent::BaseController
        before_action :authenticate_node_access!

        def show_environment_secret
          secret = EnvironmentSecret.find_by(id: params[:id])
          return render_error("not_found", "secret not found", status: :not_found) unless secret
          return render_error("forbidden", "secret not available to node", status: :forbidden) unless secret.environment_id == current_node.environment_id
          return render_error("not_found", "secret not found", status: :not_found) if secret.value.blank?

          response.set_header("Cache-Control", "no-store")
          render json: { value: secret.value }
        end

        def show_environment_bundle_tunnel_token
          bundle = EnvironmentBundle.find_by(id: params[:id])
          return render_error("not_found", "secret not found", status: :not_found) unless bundle
          return render_error("forbidden", "secret not available to node", status: :forbidden) unless bundle.id == current_node.node_bundle&.environment_bundle_id
          return render_error("not_found", "secret not found", status: :not_found) if bundle.tunnel_token.blank?

          response.set_header("Cache-Control", "no-store")
          render json: { value: bundle.tunnel_token }
        end
      end
    end
  end
end
