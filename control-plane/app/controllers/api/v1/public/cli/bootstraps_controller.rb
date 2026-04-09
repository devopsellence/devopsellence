# frozen_string_literal: true

module Api
  module V1
    module Public
      module Cli
        class BootstrapsController < Api::V1::BaseController
          rate_limit to: 5, within: 5.minutes, name: "public_cli_bootstrap", by: -> { request.remote_ip },
            with: -> { render_error("rate_limited", "too many requests", status: :too_many_requests) }

          def create
            anonymous_id = params[:anonymous_id].to_s
            anonymous_secret = params[:anonymous_secret].to_s
            client_id = params[:client_id].to_s

            return render_error("invalid_request", "missing anonymous_id") if anonymous_id.blank?
            return render_error("invalid_request", "missing anonymous_secret") if anonymous_secret.blank?
            return render_error("invalid_request", "invalid client_id") unless client_id == "cli"

            user = User.bootstrap_anonymous!(identifier: anonymous_id, raw_secret: anonymous_secret)
            token_record, raw_access, raw_refresh = ApiToken.issue!(user: user)

            render json: {
              access_token: raw_access,
              refresh_token: raw_refresh,
              token_type: "Bearer",
              expires_in: (token_record.access_expires_at - Time.current).to_i,
              account_kind: user.account_kind,
              claimed: user.human?
            }, status: :created
          rescue User::AnonymousAuthenticationError => error
            render_error("invalid_grant", error.message, status: :unauthorized)
          rescue ActiveRecord::RecordInvalid => error
            render_error("invalid_request", error.record.errors.full_messages.to_sentence, status: :unprocessable_entity)
          end
        end
      end
    end
  end
end
