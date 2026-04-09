# frozen_string_literal: true

require "test_helper"

class GcpCredentialsTest < ActiveSupport::TestCase
  test "fetches impersonated access token from iam credentials api" do
    source_credentials = Object.new
    source_credentials.stubs(:fetch_access_token).returns({ "access_token" => "source-token" })

    authorization = Struct.new(:source_credentials, :impersonation_url, :scope).new(
      source_credentials,
      "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/test@example.com:generateAccessToken",
      [ "scope-a" ]
    )

    credentials = Gcp::Credentials.allocate
    credentials.instance_variable_set(:@authorization, authorization)

    stub_request(:post, "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/test@example.com:generateAccessToken")
      .with(
        body: { "scope" => [ "scope-a" ] }.to_json,
        headers: { "Authorization" => "Bearer source-token" }
      )
      .to_return(
        status: 200,
        body: { accessToken: "impersonated-token", expireTime: 5.minutes.from_now.iso8601 }.to_json,
        headers: { "Content-Type" => "application/json" }
      )

    assert_equal "impersonated-token", credentials.access_token
    assert_equal({ "access_token" => "impersonated-token" }, credentials.fetch_access_token)
  end

  test "apply sets authorization header" do
    authorization = Object.new
    authorization.stubs(:fetch_access_token).returns({ "access_token" => "plain-token" })

    credentials = Gcp::Credentials.allocate
    credentials.instance_variable_set(:@authorization, authorization)

    metadata = credentials.apply!({})

    assert_equal "Bearer plain-token", metadata["authorization"]
    assert_equal "Bearer plain-token", metadata[:authorization]
  end

  test "returns fake access token from env when configured" do
    authorization = Object.new
    authorization.expects(:fetch_access_token).never

    credentials = Gcp::Credentials.allocate
    credentials.instance_variable_set(:@authorization, authorization)

    with_env("DEVOPSELLENCE_GCP_FAKE_ACCESS_TOKEN" => "fake-token") do
      assert_equal "fake-token", credentials.access_token
      assert_equal({ "access_token" => "fake-token" }, credentials.fetch_access_token)
    end
  end

  test "fake access token bypasses adc lookup during initialization" do
    Google::Auth.expects(:get_application_default).never

    with_env("DEVOPSELLENCE_GCP_FAKE_ACCESS_TOKEN" => "fake-token") do
      credentials = Gcp::Credentials.new(scope: "scope-a")

      assert_equal "fake-token", credentials.access_token
    end
  end
end
