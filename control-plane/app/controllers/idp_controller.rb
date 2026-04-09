# frozen_string_literal: true

class IdpController < ActionController::API
  def openid_configuration
    issuer = PublicBaseUrl.resolve(request)
    render json: {
      issuer: issuer,
      jwks_uri: "#{issuer}/.well-known/jwks.json",
      id_token_signing_alg_values_supported: [ "RS256" ],
      subject_types_supported: [ "public" ]
    }
  rescue Idp::SubjectTokenIssuer::MissingSigningKey => error
    render json: { error: "server_error", error_description: error.message }, status: :service_unavailable
  end

  def jwks
    render json: Idp::SubjectTokenIssuer.jwks
  rescue Idp::SubjectTokenIssuer::MissingSigningKey => error
    render json: { error: "server_error", error_description: error.message }, status: :service_unavailable
  end

  def desired_state_jwks
    render json: Nodes::DesiredStateEnvelope.jwks
  rescue Nodes::DesiredStateEnvelope::MissingSigningKey => error
    render json: { error: "server_error", error_description: error.message }, status: :service_unavailable
  end
end
