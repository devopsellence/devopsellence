# frozen_string_literal: true

module Runtime
  module Broker
    class StandaloneClient
      Result = LocalClient::Result
      PushAuth = LocalClient::PushAuth

      def ensure_environment_runtime!(environment:)
        Result.new(status: :ready, message: nil)
      end

      def ensure_node_runtime!(node:)
        bundle = node.node_bundle
        raise "node has no bundle" unless bundle

        node.update!(
          desired_state_object_path: bundle.desired_state_object_path,
          desired_state_sequence: [ node.desired_state_sequence, bundle.desired_state_sequence ].max,
          provisioning_status: Node::PROVISIONING_READY,
          provisioning_error: nil
        )
        Result.new(status: Node::PROVISIONING_READY, message: nil)
      rescue StandardError => error
        node.update!(
          provisioning_status: Node::PROVISIONING_FAILED,
          provisioning_error: "runtime broker provisioning failed: #{error.message}"
        )
        Result.new(status: Node::PROVISIONING_FAILED, message: node.provisioning_error)
      end

      def upsert_environment_secret!(environment_secret:, value:)
        secret_value = value.to_s
        raise ArgumentError, "secret value is required" if secret_value.blank?

        environment_secret.value = secret_value
        environment_secret.value_sha256 = EnvironmentSecret.value_sha256(secret_value)
        environment_secret.access_grantee_email = nil
        environment_secret.access_verified_at = Time.current
        environment_secret.save!
        environment_secret
      end

      def upsert_environment_ingress_secret!(environment_ingress:, value:)
        secret_value = value.to_s
        raise ArgumentError, "secret value is required" if secret_value.blank?

        bundle = environment_ingress.environment.environment_bundle
        raise "environment has no bundle" unless bundle

        bundle.update!(tunnel_token: secret_value)
        environment_ingress.save! unless environment_ingress.persisted?
        environment_ingress
      end

      def destroy_environment_secret!(environment_secret:)
        environment_secret.destroy!
        environment_secret
      end

      def ensure_environment_secret_access!(environment_secret:)
        environment_secret.update_columns(access_grantee_email: nil, access_verified_at: Time.current)
      end

      def ensure_environment_ingress_access!(environment_ingress:)
        true
      end

      def provision_organization_bundle!(bundle:)
        Result.new(status: :ready, message: nil)
      rescue StandardError => error
        Result.new(status: :failed, message: error.message)
      end

      def provision_environment_bundle!(bundle:)
        Result.new(status: :ready, message: nil)
      rescue StandardError => error
        Result.new(status: :failed, message: error.message)
      end

      def upsert_environment_bundle_tunnel_secret!(bundle:, tunnel_token:)
        bundle.update!(tunnel_token:)
        Result.new(status: :ready, message: nil)
      rescue StandardError => error
        Result.new(status: :failed, message: error.message)
      end

      def ensure_node_bundle_impersonation!(bundle:)
        Result.new(status: :ready, message: nil)
      end

      def revoke_node_bundle_impersonation!(bundle:)
        Result.new(status: :ready, message: nil)
      end

      def issue_gar_push_auth!(organization:)
        config = organization.organization_registry_config
        raise Cli::RegistryPushAuthIssuer::Error, "organization registry is not configured" unless config

        PushAuth.new(
          registry_host: config.registry_host,
          gar_repository_path: config.repository_path,
          docker_username: config.username,
          docker_password: config.password,
          expires_in: expires_in_for(config)
        )
      end

      private

      def expires_in_for(config)
        expiry = config.expires_at
        return 0 if expiry.blank?

        [ (expiry - Time.current).to_i, 0 ].max
      end
    end
  end
end
