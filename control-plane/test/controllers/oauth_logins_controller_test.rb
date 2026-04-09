# frozen_string_literal: true

require "test_helper"
require "uri"

class OauthLoginsControllerTest < ActionController::TestCase
  tests OauthLoginsController

  test "callback signs user in and redirects to getting started" do
    @request.env["omniauth.auth"] = {
      "provider" => "google_oauth2",
      "uid" => "google-123",
      "info" => { "email" => "oauth@example.com" },
      "extra" => { "raw_info" => { "email_verified" => true } }
    }

    Authentication::OauthIdentityResolver.any_instance.stubs(:call).returns(
      User.find_or_create_by!(email: "oauth@example.com").tap(&:confirm!)
    )
    without_http_basic do
      get :callback
    end

    assert_redirected_to getting_started_path(anchor: "quickstart-heading")
    assert_equal "oauth@example.com", User.find(session[:user_id]).email
  end

  test "callback completes cli login flow and redirects to loopback callback" do
    @request.env["omniauth.auth"] = {
      "provider" => "google_oauth2",
      "uid" => "google-123",
      "info" => { "email" => "oauth@example.com" },
      "extra" => { "raw_info" => { "email_verified" => true } }
    }

    user = User.find_or_create_by!(email: "oauth@example.com").tap(&:confirm!)
    Authentication::OauthIdentityResolver.any_instance.stubs(:call).returns(user)

    session[:login_redirect_uri] = "http://127.0.0.1:45678/callback"
    session[:login_state] = "state-123"
    session[:login_code_challenge] = "challenge-123"
    session[:login_code_challenge_method] = "S256"

    without_http_basic do
      get :callback
    end

    location = URI.parse(@response.location)
    params = Rack::Utils.parse_nested_query(location.query)

    assert_equal "http", location.scheme
    assert_equal "127.0.0.1", location.host
    assert_equal 45678, location.port
    assert_equal "/callback", location.path
    assert params["code"].present?
    assert_equal "state-123", params["state"]
    assert_equal "oauth@example.com", User.find(session[:user_id]).email
  end
end
