# frozen_string_literal: true

module Api
  module V1
    module Cli
      class GarController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!
        rate_limit to: 20, within: 1.minute, name: "cli_gar_push_auth", by: :cli_rate_limit_key, with: :render_rate_limited, only: :push_auth

        def push_auth
          project = find_project
          return unless project

          organization = project.organization
          unless OrganizationMembership.exists?(
            organization_id: organization.id,
            user_id: current_user_id,
            role: OrganizationMembership::ROLE_OWNER
          )
            return render_error("forbidden", "project owner role is required for registry push auth", status: :forbidden)
          end

          image_repository = params[:image_repository].to_s.strip
          return render_error("invalid_request", "image_repository is required", status: :unprocessable_entity) if image_repository.blank?

          if organization.organization_bundle.nil? && organization.gar_repository_name.blank?
            result = Gcp::OrganizationRuntimeProvisioner.new(organization: organization).call
            return render_error("provisioning_failed", result.message, status: :unprocessable_entity) unless result.status == Organization::PROVISIONING_READY
          end

          auth = ::Cli::GarPushAuthIssuer.new(organization: organization).call

          render json: {
            registry_host: auth.fetch(:registry_host),
            gar_repository_path: auth.fetch(:gar_repository_path),
            image_repository: image_repository,
            docker_username: auth.fetch(:docker_username),
            docker_password: auth.fetch(:docker_password),
            access_token: auth.fetch(:docker_password),
            expires_in: auth.fetch(:expires_in)
          }, status: :created
        end

        private

        def find_project
          project = Project.joins(:organization)
            .where(
              organizations: {
                id: OrganizationMembership.where(user_id: current_user_id).select(:organization_id)
              }
            )
            .find_by(id: params[:project_id])
          return project if project

          render_error("not_found", "project not found", status: :not_found)
          nil
        end
      end
    end
  end
end
