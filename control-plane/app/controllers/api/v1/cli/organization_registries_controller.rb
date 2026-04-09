# frozen_string_literal: true

module Api
  module V1
    module Cli
      class OrganizationRegistriesController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!

        def show
          organization = find_organization
          return render_error("forbidden", "owner role required", status: :forbidden) unless organization

          config = organization.organization_registry_config
          if config
            render json: serialize(config)
          else
            render json: { configured: false }
          end
        end

        def upsert
          organization = find_organization
          return render_error("forbidden", "owner role required", status: :forbidden) unless organization

          config = organization.organization_registry_config || organization.build_organization_registry_config
          config.registry_host = params[:registry_host].to_s.strip
          config.repository_namespace = params[:repository_namespace].to_s.strip
          config.username = params[:username].to_s.strip
          password = params[:password].to_s
          config.password = password if password.present?
          config.expires_at = parse_expires_at(params[:expires_at]) if params.key?(:expires_at)

          unless config.save
            return render_error("invalid_request", config.errors.full_messages.to_sentence, status: :unprocessable_entity)
          end

          render json: serialize(config), status: :created
        rescue ArgumentError => error
          render_error("invalid_request", error.message, status: :unprocessable_entity)
        end

        private

        def find_organization
          Organization.joins(:organization_memberships)
            .find_by(
              id: params[:organization_id],
              organization_memberships: {
                user_id: current_user_id,
                role: OrganizationMembership::ROLE_OWNER
              }
            )
        end

        def parse_expires_at(value)
          text = value.to_s.strip
          return nil if text.blank?

          Time.iso8601(text)
        rescue ArgumentError
          raise ArgumentError, "expires_at must be an ISO8601 timestamp"
        end

        def serialize(config)
          {
            configured: true,
            organization_id: config.organization_id,
            registry_host: config.registry_host,
            repository_namespace: config.repository_namespace,
            username: config.username,
            expires_at: config.expires_at&.utc&.iso8601
          }
        end
      end
    end
  end
end
