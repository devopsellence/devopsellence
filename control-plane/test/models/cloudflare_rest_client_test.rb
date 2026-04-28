# frozen_string_literal: true

require "test_helper"

module Cloudflare
  class RestClientTest < ActiveSupport::TestCase
    test "delete dns records ignores already deleted records" do
      client = RestClient.allocate
      client.instance_variable_set(:@zone_id, "zone-123")

      client.stubs(:dns_records).with(hostname: "app.devopsellence.io", type: "CNAME").returns([
        { "id" => "record-1" }
      ])
      client.expects(:request).with(:delete, "/zones/zone-123/dns_records/record-1").raises(
        RestClient::Error.new(status_code: 404, message: "cloudflare request failed (404): Record does not exist.")
      )

      assert_nothing_raised do
        client.delete_dns_records(hostname: "app.devopsellence.io", type: "CNAME")
      end
    end
  end
end
