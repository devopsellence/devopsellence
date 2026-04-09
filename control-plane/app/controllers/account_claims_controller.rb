# frozen_string_literal: true

class AccountClaimsController < ApplicationController
  layout "marketing"

  def verify
    claim_link = ClaimLink.find_valid(params[:token].to_s)
    return redirect_to(login_path, alert: "Claim link expired, request a new one.") unless claim_link

    claim_link.with_lock do
      return redirect_to(login_path, alert: "Claim link expired, request a new one.") unless claim_link.active?

      claim_link.consume!
    end

    user = claim_link.user
    if User.where.not(id: user.id).where("lower(email) = ?", claim_link.email).exists?
      return redirect_to login_path, alert: "Email already in use. Sign in instead."
    end

    user.claim!(email: claim_link.email) if user.anonymous?

    reset_session
    session[:user_id] = user.id
    redirect_to getting_started_path(anchor: "quickstart-heading"), notice: "Account claimed."
  end
end
