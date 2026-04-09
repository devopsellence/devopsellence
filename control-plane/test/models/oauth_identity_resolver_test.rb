# frozen_string_literal: true

require "test_helper"

class OauthIdentityResolverTest < ActiveSupport::TestCase
  test "google creates confirmed user and identity from verified email" do
    auth_hash = {
      "provider" => "google_oauth2",
      "uid" => "google-123",
      "info" => {
        "email" => "Owner@Example.com",
        "name" => "Owner Example"
      },
      "extra" => {
        "raw_info" => {
          "email_verified" => true
        }
      }
    }

    user = Authentication::OauthIdentityResolver.new(auth_hash: auth_hash).call

    assert_equal "owner@example.com", user.email
    assert_not_nil user.confirmed_at
    identity = user.user_identities.find_by!(provider: "google")
    assert_equal "google-123", identity.provider_uid
    assert_equal "owner@example.com", identity.email
  end

  test "google auto links to existing user by verified email" do
    user = User.create!(email: "owner-link@example.com")
    auth_hash = {
      "provider" => "google_oauth2",
      "uid" => "google-456",
      "info" => { "email" => "owner-link@example.com" },
      "extra" => { "raw_info" => { "email_verified" => true } }
    }

    resolved = Authentication::OauthIdentityResolver.new(auth_hash: auth_hash).call

    assert_equal user, resolved
    assert_not_nil user.reload.confirmed_at
    assert_equal 1, user.user_identities.count
  end

  test "github uses verified email fetcher result" do
    auth_hash = {
      "provider" => "github",
      "uid" => "github-123",
      "credentials" => { "token" => "gh-token" },
      "info" => { "name" => "Git Hub" }
    }
    fetcher = ->(token:) do
      assert_equal "gh-token", token
      "dev@example.com"
    end

    user = Authentication::OauthIdentityResolver.new(auth_hash: auth_hash, github_email_fetcher: fetcher).call

    assert_equal "dev@example.com", user.email
    assert_equal "github", user.user_identities.first.provider
  end

  test "github without verified email raises helpful error" do
    auth_hash = {
      "provider" => "github",
      "uid" => "github-404",
      "credentials" => { "token" => "gh-token" }
    }

    error = assert_raises(Authentication::OauthIdentityResolver::MissingVerifiedEmailError) do
      Authentication::OauthIdentityResolver.new(auth_hash: auth_hash, github_email_fetcher: ->(token:) {
        assert_equal "gh-token", token
        nil
      }).call
    end

    assert_equal "GitHub did not provide a verified email. Try Google sign-in instead or contact contact@devopsellence.com.", error.message
  end
end
