# frozen_string_literal: true

require "securerandom"

module Api
  module V1
    module Cli
      class EnvironmentsController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!

        def index
          project = find_project
          return unless project

          environments = project.environments
          environments = environments.where(id: params[:id].to_i) if params[:id].present?

          render json: {
            environments: environments.order(:created_at).map { |environment| serialize(environment) }
          }
        end

        def create
          project = find_owned_project
          return unless project

          organization = project.organization
          if params[:service_account_email].present?
            return render_error("invalid_request", "service_account_email is managed by devopsellence", status: :unprocessable_entity)
          end
          if organization.gcs_bucket_name.blank? || organization.gar_repository_name.blank?
            result = Gcp::OrganizationRuntimeProvisioner.new(organization: organization).call
            return render_error("provisioning_failed", result.message, status: :unprocessable_entity) unless result.status == Organization::PROVISIONING_READY
          end
          gcp_project_id = organization.gcp_project_id
          runtime_kind = if organization.trial?
            Environment::RUNTIME_MANAGED
          else
            params[:runtime_kind].to_s.strip.presence || Environment.column_defaults["runtime_kind"].presence || Environment::RUNTIME_MANAGED
          end

          environment = project.environments.new(
            name: params[:name].to_s.strip,
            gcp_project_id: gcp_project_id,
            gcp_project_number: organization.gcp_project_number,
            workload_identity_pool: organization.workload_identity_pool,
            workload_identity_provider: organization.workload_identity_provider,
            runtime_kind: runtime_kind,
            ingress_strategy: requested_ingress_strategy
          )

          unless environment.save
            return render_error("invalid_request", environment.errors.full_messages.to_sentence, status: :unprocessable_entity)
          end

          result = Gcp::EnvironmentRuntimeProvisioner.new(environment: environment).call
          return render_error("provisioning_failed", result.message, status: :unprocessable_entity) unless result.status == :ready

          render json: serialize(environment), status: :created
        end

        def update_ingress
          environment = owner_environment(params[:environment_id])
          return render_error("forbidden", "owner role required", status: :forbidden) unless environment

          if requested_ingress_strategy == Environment::INGRESS_STRATEGY_DIRECT_DNS
            incompatible_nodes = environment.assigned_web_nodes_missing_direct_dns_capability
            if incompatible_nodes.any?
              names = incompatible_nodes.map(&:name).sort.join(", ")
              return render_error("invalid_request", "assigned web nodes do not support direct_dns ingress: #{names}", status: :unprocessable_entity)
            end
          end

          environment.update!(ingress_strategy: requested_ingress_strategy)
          Environments::RepublishDesiredStateJob.perform_later(environment.id) if environment.current_release_id.present?
          EnvironmentIngresses::ReconcileJob.perform_later(environment.id)

          render json: serialize(environment)
        rescue ActiveRecord::RecordInvalid => error
          render_error("invalid_request", error.record.errors.full_messages.to_sentence, status: :unprocessable_entity)
        end

        def destroy
          environment = owner_environment(params[:environment_id])
          return render_error("forbidden", "owner role required", status: :forbidden) unless environment

          result = Environments::Delete.new(environment:).call

          render json: {
            id: result.environment_id,
            name: result.environment_name,
            project_id: result.project_id,
            project_name: result.project_name,
            customer_node_ids: result.customer_node_ids,
            managed_node_ids: result.managed_node_ids
          }
        end

        private

        def find_project
          project = member_project(params[:project_id])

          return project if project

          render_error("not_found", "project not found", status: :not_found)
          nil
        end

        def find_owned_project
          project = owner_project(params[:project_id])
          return project if project

          render_error("forbidden", "owner role required", status: :forbidden)
          nil
        end

        def serialize(environment)
          {
            id: environment.id,
            name: environment.name,
            project_id: environment.project_id,
            identity_version: environment.identity_version,
            runtime_kind: environment.runtime_kind,
            ingress_strategy: environment.ingress_strategy
          }
        end

        def requested_ingress_strategy
          params[:ingress_strategy].to_s.strip.presence ||
            Environment::INGRESS_STRATEGY_TUNNEL
        end
      end
    end
  end
end
