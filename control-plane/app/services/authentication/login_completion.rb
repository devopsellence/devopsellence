# frozen_string_literal: true

module Authentication
  class LoginCompletion
    Result = Struct.new(:user, :redirect_uri, :redirect_path, :state, :auth_code, keyword_init: true)

    class << self
      def clear_context(session)
        session.delete(:login_redirect_uri)
        session.delete(:login_state)
        session.delete(:login_code_challenge)
        session.delete(:login_code_challenge_method)
        session.delete(:login_redirect_path)
      end

      def safe_redirect_path(value)
        path = value.to_s
        return nil if path.blank?
        return nil unless path.start_with?("/")
        return nil if path.start_with?("//")

        path
      end

      def redirect_uri_with_code(uri_value, code, state)
        uri = URI.parse(uri_value)
        params = Rack::Utils.parse_nested_query(uri.query)
        params["code"] = code
        params["state"] = state if state.present?
        uri.query = params.to_query.presence
        uri.to_s
      end
    end

    def initialize(user:, session:, request:)
      @user = user
      @session = session
      @request = request
    end

    def call
      result = nil

      if cli_login?
        user.confirm! if user.confirmed_at.nil?
        _link, auth_code = LoginLink.issue_cli_auth_code!(
          user: user,
          request: request,
          redirect_uri: session[:login_redirect_uri],
          state: session[:login_state],
          code_challenge: session[:login_code_challenge],
          code_challenge_method: session[:login_code_challenge_method]
        )
        result = Result.new(
          user: user,
          redirect_uri: session[:login_redirect_uri],
          state: session[:login_state],
          auth_code: auth_code
        )
      else
        result = Result.new(user: user, redirect_path: self.class.safe_redirect_path(session[:login_redirect_path]))
      end

      self.class.clear_context(session)
      result
    end

    private

    attr_reader :user, :session, :request

    def cli_login?
      session[:login_redirect_uri].present? && session[:login_state].present? &&
        session[:login_code_challenge].present? && session[:login_code_challenge_method].present?
    end
  end
end
