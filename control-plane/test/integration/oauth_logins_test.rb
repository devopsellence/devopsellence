# frozen_string_literal: true

require "test_helper"

class OauthLoginsTest < ActionDispatch::IntegrationTest
  setup do
    OmniAuth.config.test_mode = true
  end

  teardown do
    OmniAuth.config.mock_auth[:google_oauth2] = nil
    OmniAuth.config.test_mode = false
  end

  test "login page renders configured oauth providers and no email form" do
    with_env(
      "GOOGLE_CLIENT_ID" => "google-client-id",
      "GOOGLE_CLIENT_SECRET" => "google-client-secret",
      "GITHUB_CLIENT_ID" => "github-client-id",
      "GITHUB_CLIENT_SECRET" => "github-client-secret"
    ) do
      get login_path

      assert_response :success
      assert_includes response.body, "Continue with Google"
      assert_includes response.body, "Continue with GitHub"
      assert_includes response.body, 'data-turbo="false"'
      refute_includes response.body, "Send sign-in link"
      refute_includes response.body, 'type="email"'
    end
  end
end
