# frozen_string_literal: true

require "uri"

module Api
  module V1
    module Cli
      class AccountClaimsController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!

        def create
          return render_error("forbidden", "account is already claimed", status: :forbidden) unless current_user.anonymous?

          email = params[:email].to_s.strip.downcase
          return render_error("invalid_request", "missing email") if email.blank?
          return render_error("invalid_request", "invalid email") unless email.match?(URI::MailTo::EMAIL_REGEXP)
          return render_error("invalid_request", "email is already in use") if email_in_use?(email)

          ClaimLink.where(user: current_user, consumed_at: nil).update_all(consumed_at: Time.current)
          _claim_link, raw_token = ClaimLink.issue!(user: current_user, email: email, request: request)
          LoginMailer.claim_link(current_user, email, raw_token).deliver_later

          render json: {
            status: "ok",
            email: email,
            message: "Check your email to claim your account."
          }, status: :created
        rescue ActiveRecord::RecordInvalid => error
          render_error("invalid_request", error.record.errors.full_messages.to_sentence, status: :unprocessable_entity)
        end

        private

        def email_in_use?(email)
          User.where.not(id: current_user.id).where("lower(email) = ?", email).exists?
        end
      end
    end
  end
end
