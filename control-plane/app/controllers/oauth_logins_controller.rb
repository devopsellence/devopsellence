# frozen_string_literal: true

class OauthLoginsController < ApplicationController
  def callback
    user = Authentication::OauthIdentityResolver.new(auth_hash: request.env.fetch("omniauth.auth")).call
    completion = Authentication::LoginCompletion.new(user:, session:, request:).call

    reset_session
    session[:user_id] = user.id

    if completion.redirect_uri.present?
      redirect_to Authentication::LoginCompletion.redirect_uri_with_code(completion.redirect_uri, completion.auth_code, completion.state), allow_other_host: true
    else
      redirect_to completion.redirect_path || getting_started_path(anchor: "quickstart-heading"), notice: "Signed in."
    end
  rescue Authentication::OauthIdentityResolver::MissingVerifiedEmailError,
    Authentication::OauthIdentityResolver::IdentityConflictError,
    Authentication::OauthIdentityResolver::UnsupportedProviderError => error
    redirect_to login_path, alert: error.message
  rescue KeyError
    redirect_to login_path, alert: "Sign-in failed. Please try again."
  end

  def failure
    redirect_to login_path, alert: "Sign-in failed. Please try again."
  end
end
