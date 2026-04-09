# frozen_string_literal: true

require "json"
require "openssl"
require "test_helper"

class IdpEndpointsTest < ActionDispatch::IntegrationTest
  test "openid configuration and jwks are served when signing key exists" do
    node_rsa = OpenSSL::PKey::RSA.generate(2048)
    desired_state_rsa = OpenSSL::PKey::RSA.generate(2048)
    expected_issuer = ENV["DEVOPSELLENCE_PUBLIC_BASE_URL"].to_s.strip
    expected_issuer = "http://www.example.com" if expected_issuer.empty?

    with_env(
      "DEVOPSELLENCE_IDP_PRIVATE_KEY_PEM" => node_rsa.to_pem,
      "DEVOPSELLENCE_DESIRED_STATE_PRIVATE_KEY_PEM" => desired_state_rsa.to_pem
    ) do
      get "/.well-known/openid-configuration"
      assert_response :success
      config_body = JSON.parse(response.body)
      assert_equal expected_issuer, config_body["issuer"]
      assert_equal "#{expected_issuer}/.well-known/jwks.json", config_body["jwks_uri"]

      get "/.well-known/jwks.json"
      assert_response :success
      jwks_body = JSON.parse(response.body)
      assert_equal "RSA", jwks_body.fetch("keys").first.fetch("kty")
      assert_equal 1, jwks_body.fetch("keys").size
      assert_match(/\Anode_identity:/, jwks_body.fetch("keys").first.fetch("kid"))

      get "/.well-known/devopsellence-desired-state-jwks.json"
      assert_response :success
      desired_state_body = JSON.parse(response.body)
      assert_equal 1, desired_state_body.fetch("keys").size
      assert_match(/\Adesired_state:/, desired_state_body.fetch("keys").first.fetch("kid"))
    end
  end

  test "jwks returns service unavailable when key is missing" do
    with_env("DEVOPSELLENCE_IDP_PRIVATE_KEY_PEM" => nil) do
      get "/.well-known/jwks.json"
    end

    assert_response :service_unavailable
    body = JSON.parse(response.body)
    assert_equal "server_error", body["error"]
  end

  test "desired state jwks returns service unavailable when key is missing" do
    with_env("DEVOPSELLENCE_DESIRED_STATE_PRIVATE_KEY_PEM" => nil) do
      get "/.well-known/devopsellence-desired-state-jwks.json"
    end

    assert_response :service_unavailable
    body = JSON.parse(response.body)
    assert_equal "server_error", body["error"]
  end
end
