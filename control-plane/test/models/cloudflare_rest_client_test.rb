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

    test "create dns cname treats an identical concurrent record as success" do
      client = RestClient.allocate
      client.instance_variable_set(:@zone_id, "zone-123")

      client.stubs(:dns_record_name).with("app.devopsellence.io").returns("app")
      client.stubs(:dns_records).with(hostname: "app.devopsellence.io", type: "CNAME").returns(
        [],
        [
          {
            "id" => "record-2",
            "type" => "CNAME",
            "content" => "tunnel-1.cfargotunnel.com",
            "proxied" => true
          }
        ]
      )
      client.expects(:request).with(
        :post,
        "/zones/zone-123/dns_records",
        payload: {
          type: "CNAME",
          name: "app",
          content: "tunnel-1.cfargotunnel.com",
          proxied: true
        }
      ).raises(
        RestClient::Error.new(
          status_code: 400,
          message: "cloudflare request failed (400): An A, AAAA, or CNAME record with that host already exists."
        )
      )

      result = client.create_dns_cname(hostname: "app.devopsellence.io", target: "tunnel-1.cfargotunnel.com")

      assert_equal "record-2", result.fetch("id")
    end
  end
end
