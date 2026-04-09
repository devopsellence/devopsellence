# frozen_string_literal: true

module Api
  module V1
    module Cli
      class TokensController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!
        before_action :authenticate_cli_refresh_for_create!, only: :create
        rate_limit to: 10, within: 1.minute, name: "cli_token_create", by: :cli_rate_limit_key, with: :render_rate_limited, only: :create

        def index
          render json: {
            tokens: current_user.api_tokens.order(created_at: :desc).map do |token|
              serialize(token)
            end
          }
        end

        def create
          name = params[:name].to_s.strip.presence || "deploy"
          token, raw_access = ApiToken.issue_ci_token!(user: current_user, name: name)

          render json: {
            token: raw_access,
            name: token.name,
            created_at: token.created_at.utc.iso8601
          }, status: :created
        end

        def destroy
          token = current_user.api_tokens.find_by(id: params[:id])
          return render_error("not_found", "token not found", status: :not_found) unless token

          token.revoke!

          render json: serialize(token.reload)
        end

        private

        def serialize(token)
          {
            id: token.id,
            name: token.name.presence || "session",
            created_at: token.created_at.utc.iso8601,
            last_used_at: token.last_used_at&.utc&.iso8601,
            revoked_at: token.revoked_at&.utc&.iso8601,
            current: current_api_token&.id == token.id
          }
        end

        def authenticate_cli_refresh_for_create!
          refresh_token = params[:refresh_token].to_s
          return render_error("invalid_request", "missing refresh_token", status: :unauthorized) if refresh_token.blank?

          unless current_api_token.refresh_token_digest == ApiToken.digest(refresh_token)
            return render_error("invalid_grant", "invalid refresh_token", status: :unauthorized)
          end

          return if current_api_token.refresh_active?

          render_error("invalid_grant", "refresh_token expired", status: :unauthorized)
        end
      end
    end
  end
end
