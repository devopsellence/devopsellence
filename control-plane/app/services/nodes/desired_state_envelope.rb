# frozen_string_literal: true

require "base64"
require "digest"
require "json"

module Nodes
  class DesiredStateEnvelope
    MissingSigningKey = Trust::Keyring::MissingKeyError
    FORMAT = "signed_desired_state.v1"
    SCHEMA_VERSION = 1
    TTL = 1.day

    class << self
      def jwks
        Trust::Keyring.key_id_for(Trust::Keyring::KEY_DESIRED_STATE)
        Trust::Keyring.jwks(key_names: [ Trust::Keyring::KEY_DESIRED_STATE ])
      end

      def wrap(node:, environment:, sequence:, payload:)
        payload_json = JSON.generate(payload)
        payload_sha256 = Digest::SHA256.hexdigest(payload_json)
        issued_at = Time.current.utc
        expires_at = issued_at + TTL
        environment_id = environment&.id.to_i

        bundle = node.node_bundle
        organization_bundle_token = bundle_token(bundle&.organization_bundle)
        environment_bundle_token = bundle_token(bundle&.environment_bundle)
        node_bundle_token = bundle_token(bundle)

        signing_result = Trust::Keyring.sign!(
          key_name: Trust::Keyring::KEY_DESIRED_STATE,
          data: signing_input(
            organization_bundle_token:,
            environment_bundle_token:,
            node_bundle_token:,
            node_id: node.id,
            environment_id: environment_id,
            sequence:,
            issued_at:,
            expires_at:,
            payload_sha256:
          )
        )
        key_id = signing_result.fetch(:key_id)
        signature = Base64.urlsafe_encode64(signing_result.fetch(:signature), padding: false)

        {
          format: FORMAT,
          schema_version: SCHEMA_VERSION,
          algorithm: signing_result.fetch(:algorithm),
          key_id: key_id,
          organization_bundle_token: organization_bundle_token,
          environment_bundle_token: environment_bundle_token,
          node_bundle_token: node_bundle_token,
          node_id: node.id,
          environment_id: environment_id,
          sequence: sequence,
          issued_at: issued_at.iso8601,
          expires_at: expires_at.iso8601,
          payload_sha256: payload_sha256,
          payload_json: payload_json,
          signature: signature
        }
      end

      def signing_input(organization_bundle_token:, environment_bundle_token:, node_bundle_token:, node_id:, environment_id:, sequence:, issued_at:, expires_at:, payload_sha256:)
        [
          FORMAT,
          "organization_bundle_token=#{organization_bundle_token}",
          "environment_bundle_token=#{environment_bundle_token}",
          "node_bundle_token=#{node_bundle_token}",
          "node_id=#{node_id}",
          "environment_id=#{environment_id}",
          "sequence=#{sequence}",
          "issued_at=#{normalize_time(issued_at)}",
          "expires_at=#{normalize_time(expires_at)}",
          "payload_sha256=#{payload_sha256}"
        ].join("\n")
      end

      private

      def normalize_time(value)
        time = value.is_a?(String) ? Time.iso8601(value) : value
        time.utc.iso8601
      end

      def bundle_token(bundle)
        return "" unless bundle.respond_to?(:token)

        bundle.token.to_s
      end
    end
  end
end
