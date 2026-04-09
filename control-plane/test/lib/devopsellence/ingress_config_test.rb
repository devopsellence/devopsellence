# frozen_string_literal: true

require "test_helper"

module Devopsellence
  class IngressConfigTest < ActiveSupport::TestCase
    test "local backend uses local ingress runtime values" do
      with_runtime_config(
        ingress_backend: "local",
        local_ingress_public_url: "http://127.0.0.1:18080",
        local_ingress_hostname_suffix: "local.devopsellence.test",
        cloudflare_zone_name: "devopsellence.io",
        cloudflare_envoy_origin: "http://envoy:8000"
      ) do
        assert IngressConfig.local?
        assert_not IngressConfig.managed?
        assert_equal "local.devopsellence.test", IngressConfig.hostname_zone_name
        assert_equal "http://127.0.0.1:18080", IngressConfig.public_url("ignored.devopsellence.io")
        assert_equal "http://envoy:8000", IngressConfig.envoy_origin
      end
    end

    test "managed backend uses hostname-derived https url" do
      with_runtime_config(
        ingress_backend: "cloudflare",
        local_ingress_public_url: "http://127.0.0.1:18080",
        local_ingress_hostname_suffix: "local.devopsellence.test",
        cloudflare_zone_name: "devopsellence.io",
        cloudflare_envoy_origin: "http://envoy:8000"
      ) do
        assert_not IngressConfig.local?
        assert IngressConfig.managed?
        assert_equal "devopsellence.io", IngressConfig.hostname_zone_name
        assert_equal "https://app.devopsellence.io", IngressConfig.public_url("app.devopsellence.io")
        assert_nil IngressConfig.public_url(nil)
      end
    end
  end
end
