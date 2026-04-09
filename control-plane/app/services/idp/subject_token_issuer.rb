# frozen_string_literal: true

require "base64"
require "json"
require "securerandom"

module Idp
  class SubjectTokenIssuer
    MissingSigningKey = Trust::Keyring::MissingKeyError
    TTL = 5.minutes

    class << self
      def issue!(node:, environment:, issuer:)
        now = Time.current.to_i
        exp = (Time.current + TTL).to_i
        bundle = node_bundle(node)

        payload = {
          iss: issuer,
          sub: bundle ? "node_bundle:#{bundle.token}" : "node:#{node.id}",
          aud: environment.audience,
          iat: now,
          exp: exp,
          jti: SecureRandom.uuid,
          organization_id: node.organization_id.to_s,
          node_id: node.id.to_s,
          project_id: string_attr(environment, :project_id),
          environment_id: string_attr(environment, :id),
          identity_version: integer_attr(environment, :identity_version),
          gcp_project_id: string_attr(environment, :gcp_project_id),
          gcp_project_number: string_attr(environment, :gcp_project_number),
          service_account_email: string_attr(environment, :service_account_email),
          organization_bundle_token: bundle_token(node_bundle(node)&.organization_bundle || organization_bundle(environment)),
          environment_bundle_token: bundle_token(node_bundle(node)&.environment_bundle || environment_bundle(environment)),
          node_bundle_token: bundle_token(node_bundle(node))
        }

        header = { alg: "RS256", typ: "JWT", kid: Trust::Keyring.key_id_for(Trust::Keyring::KEY_NODE_IDENTITY) }
        encoded_header = encode_component(header.to_json)
        encoded_payload = encode_component(payload.to_json)
        signing_result = Trust::Keyring.sign!(
          key_name: Trust::Keyring::KEY_NODE_IDENTITY,
          data: "#{encoded_header}.#{encoded_payload}"
        )
        encoded_signature = encode_component(signing_result.fetch(:signature))

        "#{encoded_header}.#{encoded_payload}.#{encoded_signature}"
      end

      def issue_for_bundle!(node_bundle:, issuer:)
        now = Time.current.to_i
        exp = (Time.current + TTL).to_i
        environment_bundle = node_bundle.environment_bundle

        payload = {
          iss: issuer,
          sub: "node_bundle:#{node_bundle.token}",
          aud: node_bundle.runtime_project.audience,
          iat: now,
          exp: exp,
          jti: SecureRandom.uuid,
          organization_bundle_token: bundle_token(node_bundle.organization_bundle),
          environment_bundle_token: bundle_token(environment_bundle),
          node_bundle_token: bundle_token(node_bundle)
        }

        header = { alg: "RS256", typ: "JWT", kid: Trust::Keyring.key_id_for(Trust::Keyring::KEY_NODE_IDENTITY) }
        encoded_header = encode_component(header.to_json)
        encoded_payload = encode_component(payload.to_json)
        signing_result = Trust::Keyring.sign!(
          key_name: Trust::Keyring::KEY_NODE_IDENTITY,
          data: "#{encoded_header}.#{encoded_payload}"
        )
        encoded_signature = encode_component(signing_result.fetch(:signature))

        "#{encoded_header}.#{encoded_payload}.#{encoded_signature}"
      end

      def jwks
        Trust::Keyring.key_id_for(Trust::Keyring::KEY_NODE_IDENTITY)
        Trust::Keyring.jwks(key_names: [ Trust::Keyring::KEY_NODE_IDENTITY ])
      end

      private

      def encode_component(value)
        Base64.urlsafe_encode64(value, padding: false)
      end

      def string_attr(object, method_name)
        return "" unless object.respond_to?(method_name)

        object.public_send(method_name).to_s
      end

      def integer_attr(object, method_name)
        return "0" unless object.respond_to?(method_name)

        object.public_send(method_name).to_i.to_s
      end

      def node_bundle(node)
        return unless node.respond_to?(:node_bundle)

        node.node_bundle
      end

      def environment_bundle(environment)
        return environment if environment.is_a?(EnvironmentBundle)
        return unless environment.respond_to?(:environment_bundle)

        environment.environment_bundle
      end

      def organization_bundle(environment)
        return environment.organization_bundle if environment.respond_to?(:organization_bundle)

        nil
      end

      def bundle_token(bundle)
        return "" unless bundle.respond_to?(:token)

        bundle.token.to_s
      end
    end
  end
end
