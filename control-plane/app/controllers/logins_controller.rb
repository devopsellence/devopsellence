# frozen_string_literal: true

class LoginsController < ApplicationController
  layout "marketing"

  OAUTH_SIGN_IN_MESSAGE = "Use GitHub or Google sign-in to continue."

  def new
    store_cli_context_from_params
    session[:login_redirect_path] = Authentication::LoginCompletion.safe_redirect_path(params[:redirect_path])
  end

  def create
    redirect_to login_path, alert: OAUTH_SIGN_IN_MESSAGE
  end

  def verify
    token = params[:token].to_s
    login_link = LoginLink.find_valid(token)

    unless login_link
      return redirect_to login_path, alert: "Link expired, request a new one."
    end

    login_link.with_lock do
      return redirect_to(login_path, alert: "Link expired, request a new one.") unless login_link.active?

      login_link.consume!
    end

    hydrate_login_context_from(login_link)
    user = login_link.user
    completion = Authentication::LoginCompletion.new(user:, session:, request:).call

    reset_session
    session[:user_id] = user.id

    if completion.redirect_uri.present?
      redirect_to Authentication::LoginCompletion.redirect_uri_with_code(completion.redirect_uri, completion.auth_code, completion.state), allow_other_host: true
    else
      redirect_to completion.redirect_path || getting_started_path(anchor: "quickstart-heading"), notice: "Signed in."
    end
  end

  private

  def store_cli_context_from_params
    return unless params[:redirect_uri].present?

    redirect_uri = LoginLink.safe_redirect_uri(params[:redirect_uri])
    state = params[:state].to_s
    code_challenge = params[:code_challenge].to_s
    code_challenge_method = params[:code_challenge_method].presence || "S256"

    if redirect_uri.present? && state.present? && code_challenge.present?
      session[:login_redirect_uri] = redirect_uri
      session[:login_state] = state
      session[:login_code_challenge] = code_challenge
      session[:login_code_challenge_method] = code_challenge_method
    else
      Authentication::LoginCompletion.clear_context(session)
      flash.now[:alert] = "Invalid login parameters."
    end
  end

  def hydrate_login_context_from(login_link)
    session[:login_redirect_uri] = login_link.redirect_uri
    session[:login_state] = login_link.state
    session[:login_code_challenge] = login_link.code_challenge
    session[:login_code_challenge_method] = login_link.code_challenge_method
    session[:login_redirect_path] = login_link.redirect_path
  end
end
