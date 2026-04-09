# frozen_string_literal: true

require "test_helper"

class GcpRestClientTest < ActiveSupport::TestCase
  test "get parses string uri before issuing request" do
    client = Gcp::RestClient.allocate
    credentials = Object.new
    client.instance_variable_set(:@credentials, credentials)

    credentials.stubs(:authorization_header).returns("Bearer token-123")

    stub_request(:get, "https://example.com/v1/test")
      .with(headers: { "Authorization" => "Bearer token-123" })
      .to_return(status: 200, body: "{}")

    response = client.get("https://example.com/v1/test")

    assert_equal "200", response.code
  end
end
