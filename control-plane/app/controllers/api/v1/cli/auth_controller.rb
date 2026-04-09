# frozen_string_literal: true

require "uri"

module Api
  module V1
    module Cli
      class AuthController < Api::V1::BaseController
        # Protect the browser-login handoff endpoint from abuse
        rate_limit to: 5, within: 1.minute, name: "auth_start", by: -> { request.remote_ip },
          with: -> { render_error("rate_limited", "too many requests", status: :too_many_requests) },
          only: :start

        # Prevent brute-force against PKCE auth codes
        rate_limit to: 10, within: 1.minute, name: "auth_token", by: -> { request.remote_ip },
          with: -> { render_error("rate_limited", "too many requests", status: :too_many_requests) },
          only: :token

        BROWSER_LOGIN_MESSAGE = "Open the browser login URL and continue with GitHub or Google."

        def start
          redirect_uri = LoginLink.safe_redirect_uri(params[:redirect_uri])
          state = params[:state].to_s
          code_challenge = params[:code_challenge].to_s
          code_challenge_method = params[:code_challenge_method].presence || "S256"
          client_id = params[:client_id].to_s

          return render_error("invalid_request", "missing redirect_uri") if params[:redirect_uri].blank?
          return render_error("invalid_request", "invalid redirect_uri") unless redirect_uri.present?
          return render_error("invalid_request", "missing state") if state.blank?
          return render_error("invalid_request", "missing code_challenge") if code_challenge.blank?
          return render_error("invalid_request", "invalid code_challenge_method") unless code_challenge_method == "S256"
          return render_error("invalid_request", "invalid client_id") unless client_id == "cli"

          render json: {
            status: "ok",
            login_url: browser_login_url(
              redirect_uri: redirect_uri,
              state: state,
              code_challenge: code_challenge,
              code_challenge_method: code_challenge_method
            ),
            message: BROWSER_LOGIN_MESSAGE
          }, status: :created
        end

        def token
          code = params[:code].to_s
          redirect_uri = params[:redirect_uri].to_s
          code_verifier = params[:code_verifier].to_s
          client_id = params[:client_id].to_s

          return render_error("invalid_request", "missing code") if code.blank?
          return render_error("invalid_request", "missing redirect_uri") if redirect_uri.blank?
          return render_error("invalid_request", "missing code_verifier") if code_verifier.blank?
          return render_error("invalid_request", "invalid client_id") unless client_id == "cli"

          login_link = LoginLink.find_by_auth_code(code)
          return render_error("invalid_grant", "invalid code", status: :unauthorized) unless login_link

          safe_uri = LoginLink.safe_redirect_uri(redirect_uri)
          unless safe_uri.present? && safe_uri == login_link.redirect_uri
            return render_error("invalid_grant", "redirect_uri mismatch", status: :unauthorized)
          end

          token_record = nil
          raw_access = nil
          raw_refresh = nil

          login_link.with_lock do
            unless login_link.auth_code_active?
              return render_error("invalid_grant", "code expired", status: :unauthorized)
            end

            unless login_link.valid_code_verifier?(code_verifier)
              return render_error("invalid_grant", "invalid code_verifier", status: :unauthorized)
            end

            login_link.consume_auth_code!
            token_record, raw_access, raw_refresh = ApiToken.issue!(user: login_link.user)
          end

          render json: {
            access_token: raw_access,
            refresh_token: raw_refresh,
            token_type: "Bearer",
            expires_in: (token_record.access_expires_at - Time.current).to_i,
            account_kind: login_link.user.account_kind
          }
        end

        def refresh
          refresh_token = params[:refresh_token].to_s
          client_id = params[:client_id].to_s

          return render_error("invalid_request", "missing refresh_token") if refresh_token.blank?
          return render_error("invalid_request", "invalid client_id") unless client_id == "cli"

          token_record = ApiToken.find_by_refresh_token(refresh_token)
          return render_error("invalid_grant", "invalid refresh_token", status: :unauthorized) unless token_record

          raw_access = nil
          raw_refresh = nil

          token_record.with_lock do
            unless token_record.refresh_active?
              return render_error("invalid_grant", "refresh_token expired", status: :unauthorized)
            end

            unless token_record.refresh_token_digest == ApiToken.digest(refresh_token)
              return render_error("invalid_grant", "refresh_token reused", status: :unauthorized)
            end

            raw_access, raw_refresh = token_record.rotate!
          end

          render json: {
            access_token: raw_access,
            refresh_token: raw_refresh,
            token_type: "Bearer",
            expires_in: (token_record.access_expires_at - Time.current).to_i,
            account_kind: token_record.user.account_kind
          }
        end

        private
          def browser_login_url(redirect_uri:, state:, code_challenge:, code_challenge_method:)
            uri = URI.join("#{PublicBaseUrl.resolve(request)}/", "cli/login")
            uri.query = {
              redirect_uri: redirect_uri,
              state: state,
              code_challenge: code_challenge,
              code_challenge_method: code_challenge_method
            }.to_query
            uri.to_s
          end
      end
    end
  end
end
