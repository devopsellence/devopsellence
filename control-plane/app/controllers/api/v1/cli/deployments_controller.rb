# frozen_string_literal: true

module Api
  module V1
    module Cli
      class DeploymentsController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!

        def show
          deployment = find_deployment
          return unless deployment

          render json: serialize_deployment(deployment)
        end

        private

        def find_deployment
          deployment = Deployment.joins(environment: { project: :organization })
            .where(organizations: { id: OrganizationMembership.where(user_id: current_user_id).select(:organization_id) })
            .includes(:release, deployment_node_statuses: :node, environment: :environment_ingress)
            .find_by(id: params[:id])
          return deployment if deployment

          render_error("not_found", "deployment not found", status: :not_found)
          nil
        end

        def serialize_deployment(deployment)
          rows = deployment.deployment_node_statuses.to_a.sort_by { |row| [ row.node.name.to_s, row.node.id ] }
          counts = rows.each_with_object(Hash.new(0)) { |row, memo| memo[row.phase] += 1 }
          total = rows.length
          pending = counts.fetch(DeploymentNodeStatus::PHASE_PENDING, 0)
          reconciling = counts.fetch(DeploymentNodeStatus::PHASE_RECONCILING, 0)
          settled = counts.fetch(DeploymentNodeStatus::PHASE_SETTLED, 0)
          error_count = counts.fetch(DeploymentNodeStatus::PHASE_ERROR, 0)
          active = pending + reconciling
          complete = total.positive? && settled == total
          failed = deployment.status == Deployment::STATUS_FAILED
          active = deployment.status == Deployment::STATUS_SCHEDULING || active.positive?

          environment = deployment.environment
          release = deployment.release

          {
            id: deployment.id,
            sequence: deployment.sequence,
            status: deployment.status,
            status_message: deployment.status_message,
            error_message: deployment.error_message,
            published_at: deployment.published_at&.utc&.iso8601,
            finished_at: deployment.finished_at&.utc&.iso8601,
            environment: {
              id: deployment.environment_id,
              name: environment.name
            },
            release: {
              id: deployment.release_id,
              revision: release.revision,
              git_sha: release.git_sha,
              image_repository: release.image_repository,
              image_digest: release.image_digest,
              services: release.services_config,
              tasks: release.tasks_config
            },
            release_task: serialize_release_task(deployment),
            summary: {
              assigned_nodes: total,
              pending: pending,
              reconciling: reconciling,
              settled: settled,
              error: error_count,
              active: active,
              complete: complete,
              failed: failed
            },
            nodes: rows.map do |row|
              {
                id: row.node_id,
                name: row.node.name,
                labels: row.node.labels,
              phase: row.phase,
              message: row.message,
              error: row.error_message,
              reported_at: row.reported_at&.utc&.iso8601,
              environments: row.environments
            }
          end,
            ingress: serialize_ingress(environment.environment_ingress)
          }
        end

        def serialize_ingress(ingress)
          return nil unless ingress

          {
            hostname: ingress.primary_hostname,
            hosts: ingress.hosts,
            public_url: ingress.public_url,
            public_urls: ingress.public_urls,
            status: ingress.status
          }
        end

        def serialize_release_task(deployment)
          return nil unless deployment.release.has_release_task?

          executor = deployment.release_task_node
          {
            status: deployment.release_task_status,
            executor_node_id: executor&.id,
            executor_node_name: executor&.name
          }
        end
      end
    end
  end
end
