# frozen_string_literal: true

class LoginMailer < ApplicationMailer
  def magic_link(user, token)
    @user = user
    @token = token

    mail(
      to: user.email,
      subject: "Finish signing in to devopsellence"
    )
  end

  def claim_link(user, email, token)
    @user = user
    @token = token
    @claim_email = email

    mail(
      to: email,
      subject: "Claim your devopsellence account"
    )
  end
end
