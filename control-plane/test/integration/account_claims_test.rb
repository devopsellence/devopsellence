# frozen_string_literal: true

require "json"
require "ostruct"
require "test_helper"

class AccountClaimsTest < ActionDispatch::IntegrationTest
  include ActiveJob::TestHelper

  setup do
    clear_enqueued_jobs
    clear_performed_jobs
    ActionMailer::Base.deliveries.clear
  end

  test "anonymous user can start claim flow and verify it" do
    user = User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")

    assert_difference("ClaimLink.count", 1) do
      assert_enqueued_jobs 1 do
        post "/api/v1/cli/account/claim/start",
          params: { email: "claim@example.com" },
          headers: auth_headers_for(user),
          as: :json
      end
    end

    assert_response :created

    perform_enqueued_jobs
    mail = ActionMailer::Base.deliveries.last
    assert mail.present?

    token = mail.body.encoded[%r{claim/verify\?token=([0-9a-f]+)}, 1]
    assert token.present?

    without_http_basic do
      get "/claim/verify", params: { token: token }
    end

    assert_redirected_to getting_started_path(anchor: "quickstart-heading")
    assert_equal "claim@example.com", user.reload.email
    assert user.human?
    assert_not_nil user.claimed_at
    assert_nil user.anonymous_identifier
  end

  test "claim verify rejects an email already used by another account" do
    user = User.bootstrap_anonymous!(identifier: "anon-123", raw_secret: "secret-123")
    User.create!(email: "taken@example.com", confirmed_at: Time.current)
    claim_link, raw_token = ClaimLink.issue!(user: user, email: "taken@example.com", request: OpenStruct.new(remote_ip: "127.0.0.1", user_agent: "test"))

    without_http_basic do
      get "/claim/verify", params: { token: raw_token }
    end

    assert_redirected_to login_path
    assert_equal "taken@example.com", claim_link.reload.email
    assert user.reload.anonymous?
  end

  private

  def auth_headers_for(user)
    _record, access_token, _refresh_token = ApiToken.issue!(user: user)
    { "Authorization" => "Bearer #{access_token}" }
  end
end
