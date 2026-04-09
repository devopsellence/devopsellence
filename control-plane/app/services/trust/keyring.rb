# frozen_string_literal: true

require "base64"
require "digest"
require "json"
require "openssl"

module Trust
  module Keyring
    KEY_NODE_IDENTITY = :node_identity
    KEY_DESIRED_STATE = :desired_state
    BACKEND_LOCAL = "local"
    BACKEND_GCP_KMS = "gcp_kms"
    MissingKeyError = Class.new(StandardError)

    module_function

    def sign!(key_name:, data:)
      backend.sign!(key_name:, data:)
    end

    def jwks(key_names: nil)
      { keys: backend.jwks(key_names: key_names) }
    end

    def key_id_for(key_name)
      backend.key_id_for(key_name)
    end

    def reset!
      @backend = nil
    end

    def backend
      @backend ||= build_backend
    end

    def build_backend
      backend_name = ENV.fetch("DEVOPSELLENCE_SIGNING_BACKEND", "").to_s.strip
      if backend_name == BACKEND_GCP_KMS ||
          ENV["DEVOPSELLENCE_IDP_SIGNING_KEY_VERSION"].to_s.present? ||
          ENV["DEVOPSELLENCE_DESIRED_STATE_SIGNING_KEY_VERSION"].to_s.present?
        GcpKmsBackend.new
      else
        LocalBackend.new
      end
    end

    def env_var_for(key_name)
      case key_name
      when KEY_NODE_IDENTITY
        "DEVOPSELLENCE_IDP_PRIVATE_KEY_PEM"
      when KEY_DESIRED_STATE
        "DEVOPSELLENCE_DESIRED_STATE_PRIVATE_KEY_PEM"
      else
        raise MissingKeyError, "unknown signing key #{key_name.inspect}"
      end
    end

    def kms_env_var_for(key_name)
      case key_name
      when KEY_NODE_IDENTITY
        "DEVOPSELLENCE_IDP_SIGNING_KEY_VERSION"
      when KEY_DESIRED_STATE
        "DEVOPSELLENCE_DESIRED_STATE_SIGNING_KEY_VERSION"
      else
        raise MissingKeyError, "unknown signing key #{key_name.inspect}"
      end
    end

    def jwk_for_public_key(public_key, key_name:)
      {
        kty: "RSA",
        alg: "RS256",
        use: "sig",
        kid: key_id_for_public_key(public_key, key_name:),
        n: encode_component(public_key.n.to_s(2)),
        e: encode_component(public_key.e.to_s(2))
      }
    end

    def key_id_for_public_key(public_key, key_name:)
      "#{key_name}:#{Digest::SHA256.hexdigest(public_key.to_der)[0, 16]}"
    end

    def encode_component(value)
      Base64.urlsafe_encode64(value, padding: false)
    end

    class LocalBackend
      def key_id_for(key_name)
        Keyring.key_id_for_public_key(private_key_for(key_name).public_key, key_name:)
      end

      def sign!(key_name:, data:)
        private_key = private_key_for(key_name)
        {
          algorithm: "RS256",
          key_id: key_id_for(key_name),
          signature: private_key.sign(OpenSSL::Digest::SHA256.new, data.to_s)
        }
      end

      def jwks(key_names: nil)
        selected_key_names(key_names).filter_map do |key_name|
          public_key = private_key_for(key_name).public_key
          Keyring.jwk_for_public_key(public_key, key_name:)
        rescue MissingKeyError
          nil
        end
      end

      private

      def selected_key_names(key_names)
        Array(key_names.presence || [ KEY_NODE_IDENTITY, KEY_DESIRED_STATE ])
      end

      def private_key_for(key_name)
        pem = ENV[Keyring.env_var_for(key_name)].to_s
        raise MissingKeyError, "missing #{Keyring.env_var_for(key_name)}" if pem.blank?

        OpenSSL::PKey::RSA.new(pem)
      rescue OpenSSL::PKey::RSAError
        raise MissingKeyError, "invalid #{Keyring.env_var_for(key_name)}"
      end
    end

    class GcpKmsBackend
      def initialize(client: nil)
        @client = client || Gcp::RestClient.new
        @public_keys = {}
      end

      def key_id_for(key_name)
        Keyring.key_id_for_public_key(public_key_for(key_name), key_name:)
      end

      def sign!(key_name:, data:)
        key_version = key_version_for(key_name)
        response = client.post(
          "#{Gcp::Endpoints.cloud_kms_base}/#{key_version}:asymmetricSign",
          payload: {
            digest: {
              sha256: Base64.strict_encode64(Digest::SHA256.digest(data.to_s))
            }
          }
        )
        raise MissingKeyError, "kms asymmetric sign failed (#{response.code})" unless response.code.to_i.between?(200, 299)

        body = JSON.parse(response.body.presence || "{}")
        {
          algorithm: "RS256",
          key_id: key_id_for(key_name),
          signature: Base64.strict_decode64(body.fetch("signature"))
        }
      end

      def jwks(key_names: nil)
        selected_key_names(key_names).filter_map do |key_name|
          public_key = public_key_for(key_name)
          Keyring.jwk_for_public_key(public_key, key_name:)
        rescue MissingKeyError
          nil
        end
      end

      private

      attr_reader :client

      def selected_key_names(key_names)
        Array(key_names.presence || [ KEY_NODE_IDENTITY, KEY_DESIRED_STATE ])
      end

      def public_key_for(key_name)
        @public_keys[key_name] ||= begin
          key_version = key_version_for(key_name)
          response = client.get("#{Gcp::Endpoints.cloud_kms_base}/#{key_version}/publicKey")
          raise MissingKeyError, "kms public key fetch failed (#{response.code})" unless response.code.to_i.between?(200, 299)

          body = JSON.parse(response.body.presence || "{}")
          OpenSSL::PKey::RSA.new(body.fetch("pem"))
        end
      end

      def key_version_for(key_name)
        value = ENV[Keyring.kms_env_var_for(key_name)].to_s.strip
        raise MissingKeyError, "missing #{Keyring.kms_env_var_for(key_name)}" if value.blank?

        value
      end
    end
  end
end
