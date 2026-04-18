# frozen_string_literal: true

module Api
  module V1
    module Cli
      class EnvironmentSecretsController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!
        rate_limit to: 30, within: 1.minute, name: "cli_environment_secret_writes", by: :cli_rate_limit_key, with: :render_rate_limited, only: [ :create, :destroy ]

        def index
          environment = find_environment
          return render_error("forbidden", "owner role required", status: :forbidden) unless environment

          render json: {
            secrets: environment.environment_secrets.order(:service_name, :name).map { |secret| serialize(secret) }
          }
        end

        def create
          environment = find_environment
          return render_error("forbidden", "owner role required", status: :forbidden) unless environment

          environment_secret = environment.environment_secrets.find_or_initialize_by(
            service_name: normalized_service_name,
            name: params[:name].to_s.strip
          )
          value = params[:value].to_s
          return render_error("invalid_request", "secret value is required", status: :unprocessable_entity) if value.blank?

          Gcp::EnvironmentSecretManager.new.upsert!(environment_secret: environment_secret, value: value)

          render json: serialize(environment_secret), status: :created
        rescue ActiveRecord::RecordInvalid => error
          render_error("invalid_request", error.record.errors.full_messages.to_sentence, status: :unprocessable_entity)
        rescue ArgumentError => error
          render_error("invalid_request", error.message, status: :unprocessable_entity)
        rescue StandardError => error
          render_error("secret_save_failed", error.message, status: :unprocessable_entity)
        end

        def destroy
          environment = find_environment
          return render_error("forbidden", "owner role required", status: :forbidden) unless environment

          environment_secret = environment.environment_secrets.find_by(
            service_name: normalized_service_name,
            name: params[:name].to_s.strip
          )
          return render_error("not_found", "secret not found", status: :not_found) unless environment_secret

          Gcp::EnvironmentSecretManager.new.destroy!(environment_secret: environment_secret)

          render json: serialize(environment_secret)
        rescue StandardError => error
          render_error("secret_delete_failed", error.message, status: :unprocessable_entity)
        end

        private

        def find_environment
          Environment.joins(project: :organization)
            .where(
              organizations: {
                id: OrganizationMembership.where(
                  user_id: current_user_id,
                  role: OrganizationMembership::ROLE_OWNER
                ).select(:organization_id)
              }
            )
            .find_by(id: params[:environment_id])
        end

        def normalized_service_name
          EnvironmentSecret.normalize_service_name_value(params[:service_name])
        end

        def serialize(environment_secret)
          {
            id: environment_secret.id,
            environment_id: environment_secret.environment_id,
            service_name: environment_secret.service_name,
            name: environment_secret.name,
            gcp_secret_name: environment_secret.gcp_secret_name,
            secret_ref: environment_secret.secret_ref,
            value_sha256: environment_secret.value_sha256,
            updated_at: environment_secret.updated_at&.utc&.iso8601
          }
        end
      end
    end
  end
end
