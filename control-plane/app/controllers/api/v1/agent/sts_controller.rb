# frozen_string_literal: true

module Api
  module V1
    module Agent
      class StsController < Api::V1::Agent::BaseController
        before_action :authenticate_node_access!

        def create
          environment = current_node.node_bundle&.environment_bundle || current_node.environment
          return render_error("invalid_target", "node has no runtime identity", status: :forbidden) unless environment
          return render_error("invalid_target", "environment runtime identity is missing", status: :forbidden) if environment.service_account_email.blank?

          subject_token = Idp::SubjectTokenIssuer.issue!(
            node: current_node,
            environment: environment,
            issuer: PublicBaseUrl.resolve(request)
          )

          render json: {
            subject_token: subject_token,
            subject_token_type: "urn:ietf:params:oauth:token-type:jwt",
            audience: environment.audience,
            expires_in: Idp::SubjectTokenIssuer::TTL.to_i
          }
        rescue Idp::SubjectTokenIssuer::MissingSigningKey => error
          render_error("server_error", error.message, status: :service_unavailable)
        end
      end
    end
  end
end
