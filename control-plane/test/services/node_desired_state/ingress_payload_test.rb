# frozen_string_literal: true

require "test_helper"

module NodeDesiredState
  class IngressPayloadTest < ActiveSupport::TestCase
    test "rejects explicit null redirect_http instead of emitting null desired state" do
      release = Struct.new(:ingress_config).new({ "redirect_http" => nil })

      error = assert_raises(Release::InvalidRuntimeConfig) do
        IngressPayload.configured_redirect_http(release)
      end

      assert_equal "ingress.redirect_http must be a boolean", error.message
    end

    test "builds routes from explicit rule targets" do
      release = Struct.new(:ingress_config).new(
        {
          "rules" => [
            {
              "match" => { "host" => "APP.EXAMPLE.COM", "path_prefix" => "/api" },
              "target" => { "service" => "api", "port" => "http" }
            }
          ]
        }
      )
      environment = Struct.new(:name).new("Production")
      ingress = Struct.new(:hostname).new("bundle.example.test")

      assert_equal [
        {
          match: { hostname: "app.example.com", pathPrefix: "/api" },
          target: { environment: "Production", service: "api", port: "http" }
        }
      ], IngressPayload.routes_for(environment:, ingress:, release:)
    end

    test "build includes provisioned ingress hosts alongside configured release hosts" do
      release = Struct.new(:ingress_config).new(
        {
          "hosts" => [ "App.Example.com" ],
          "rules" => [
            {
              "match" => { "host" => "app.example.com", "path_prefix" => "/" },
              "target" => { "service" => "web", "port" => "http" }
            }
          ]
        }
      )
      def release.ingress_target_service_names = [ "web" ]
      def release.ingress_scheduled_on?(_node) = true

      ingress = Struct.new(:hostname, :hosts, :status).new(
        "bundle.example.test",
        [ "bundle.example.test", "App.Example.com" ],
        EnvironmentIngress::STATUS_READY
      )
      environment = Struct.new(:environment_ingress, :name).new(ingress, "Production")
      node = Object.new
      def node.supports_capability?(capability) = capability == Node::CAPABILITY_DIRECT_DNS_INGRESS

      payload = IngressPayload.build(node:, environment:, release:)

      assert_equal [ "bundle.example.test", "app.example.com" ], payload.fetch(:hosts)
      assert_equal "public", payload.fetch(:mode)
    end

    test "rejects malformed route targets instead of emitting invalid desired state" do
      release = Struct.new(:ingress_config).new(
        {
          "rules" => [
            {
              "match" => { "host" => "app.example.com" },
              "target" => { "service" => "web" }
            }
          ]
        }
      )
      environment = Struct.new(:name).new("Production")
      ingress = Struct.new(:hostname).new("bundle.example.test")

      error = assert_raises(Release::InvalidRuntimeConfig) do
        IngressPayload.routes_for(environment:, ingress:, release:)
      end

      assert_equal "ingress.rules[0].target.port is required", error.message
    end
  end
end
