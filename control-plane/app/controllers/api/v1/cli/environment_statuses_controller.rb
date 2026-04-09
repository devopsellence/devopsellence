# frozen_string_literal: true

module Api
  module V1
    module Cli
      class EnvironmentStatusesController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!

        def show
          environment = find_environment
          return unless environment

          organization = environment.project.organization
          current_release = environment.current_release
          latest_deployment = environment.deployments.order(created_at: :desc).first
          assigned_nodes = environment.nodes.count

          render json: {
            organization: {
              id: organization.id,
              name: organization.name
            },
            project: {
              id: environment.project_id,
              name: environment.project.name
            },
            environment: {
              id: environment.id,
              name: environment.name,
              runtime_kind: environment.runtime_kind,
              ingress_strategy: environment.ingress_strategy
            },
            ingress: serialize_ingress(environment.environment_ingress),
            current_release: serialize_release(current_release, organization),
            latest_deployment: serialize_deployment(latest_deployment),
            assigned_nodes: assigned_nodes,
            warning: assignment_warning_for(environment, assigned_nodes),
            trial_expires_at: environment.nodes.maximum(:lease_expires_at)&.utc&.iso8601
          }
        end

        private

        def find_environment
          environment = Environment.joins(project: :organization)
            .where(organizations: { id: current_user.organizations.select(:id) })
            .includes(:current_release, :deployments, project: :organization)
            .find_by(id: params[:environment_id])
          return environment if environment

          render_error("not_found", "environment not found", status: :not_found)
          nil
        end

        def serialize_release(release, organization)
          return nil unless release

          {
            id: release.id,
            revision: release.revision,
            git_sha: release.git_sha,
            image_repository: release.image_repository,
            image_digest: release.image_digest,
            image_reference: release.image_reference_for(organization),
            published_at: release.published_at&.utc&.iso8601
          }
        end

        def serialize_deployment(deployment)
          return nil unless deployment

          {
            id: deployment.id,
            sequence: deployment.sequence,
            status: deployment.status,
            status_message: deployment.status_message,
            published_at: deployment.published_at&.utc&.iso8601,
            finished_at: deployment.finished_at&.utc&.iso8601,
            error_message: deployment.error_message
          }
        end

        def serialize_ingress(ingress)
          return nil unless ingress

          {
            hostname: ingress.hostname,
            public_url: ingress.public_url,
            status: ingress.status,
            last_error: ingress.last_error
          }
        end

        def assignment_warning_for(environment, assigned_nodes)
          return nil unless environment.customer_nodes_runtime?
          return nil unless assigned_nodes.zero?

          "No customer-managed nodes are assigned to this environment yet. Run `devopsellence node register` to register and auto-attach a node, or `devopsellence node attach <id>` to attach an existing node."
        end
      end
    end
  end
end
