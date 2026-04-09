# frozen_string_literal: true

require "openssl"

module Devopsellence
  module DevelopmentEnvDefaults
    ENV = {
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "http://localhost:3000",
      "DEVOPSELLENCE_INGRESS_BACKEND" => "local",
      "DEVOPSELLENCE_LOCAL_INGRESS_PUBLIC_URL" => "http://localhost:3000",
      "DEVOPSELLENCE_SIGNING_BACKEND" => "local",
      "DEVOPSELLENCE_MANAGED_POOL_TARGET" => "0",
      "DEVOPSELLENCE_ORGANIZATION_BUNDLE_TARGET" => "0",
      "DEVOPSELLENCE_ENVIRONMENT_BUNDLE_TARGET" => "0",
      "DEVOPSELLENCE_NODE_BUNDLE_TARGET" => "0",
      "DEVOPSELLENCE_MANAGED_MAX_TOTAL" => "0",
      "DEVOPSELLENCE_IDP_PRIVATE_KEY_PEM" => OpenSSL::PKey::RSA.generate(2048).to_pem,
      "DEVOPSELLENCE_DESIRED_STATE_PRIVATE_KEY_PEM" => OpenSSL::PKey::RSA.generate(2048).to_pem
    }.freeze
  end
end
