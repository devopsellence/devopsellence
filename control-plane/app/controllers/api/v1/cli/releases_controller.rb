# frozen_string_literal: true

module Api
  module V1
    module Cli
      class ReleasesController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!
        rate_limit to: 20, within: 1.minute, name: "cli_release_create", by: :cli_rate_limit_key, with: :render_rate_limited, only: :create
        rate_limit to: 20, within: 1.minute, name: "cli_release_publish", by: :cli_rate_limit_key, with: :render_rate_limited, only: :publish

        def create
          project = find_owned_project
          return unless project

          if params[:desired_state_json].present?
            return render_error("invalid_request", "desired_state_json is no longer accepted; send explicit runtime fields", status: :unprocessable_entity)
          end

          release = project.releases.new(release_runtime_attributes)

          unless release.save
            return render_error("invalid_request", release.errors.full_messages.to_sentence, status: :unprocessable_entity)
          end

          render json: {
            id: release.id,
            project_id: release.project_id,
            status: release.status,
            revision: release.revision,
            git_sha: release.git_sha,
            image_digest: release.image_digest,
            image_repository: release.image_repository
          }, status: :created
        rescue Releases::RuntimeAttributes::InvalidPayload => error
          render_error("invalid_request", error.message, status: :unprocessable_entity)
        end

        def publish
          release = find_owned_release
          return unless release

          environment = release.project.environments.find_by(id: params[:environment_id])
          return render_error("not_found", "environment not found", status: :not_found) unless environment

          result = Deployments::Scheduler.new(
            environment: environment,
            release: release,
            request_token: publish_request_token
          ).call
          deployment = result.deployment
          assigned_nodes = environment.nodes.count

          render json: {
            deployment_id: deployment.id,
            environment_id: environment.id,
            release_id: release.id,
            sequence: deployment.sequence,
            status: deployment.status,
            status_message: deployment.status_message,
            assigned_nodes: assigned_nodes,
            warning: assignment_warning_for(environment, assigned_nodes),
            trial_expires_at: environment.nodes.maximum(:lease_expires_at)&.utc&.iso8601,
            ingress_strategy: environment.ingress_strategy,
            hostname: environment.environment_ingress&.hostname,
            public_url: environment.environment_ingress&.public_url,
            ingress_status: environment.environment_ingress&.status,
            ingress: serialize_ingress(environment.environment_ingress)
          }, status: :created
        end

        private

        def find_project
          project = member_project(params[:project_id])
          return project if project

          render_error("not_found", "project not found", status: :not_found)
          nil
        end

        def find_release
          release = member_release(params[:id])
          return release if release

          render_error("not_found", "release not found", status: :not_found)
          nil
        end

        def find_owned_project
          project = owner_project(params[:project_id])
          return project if project

          render_error("forbidden", "owner role required", status: :forbidden)
          nil
        end

        def find_owned_release
          release = owner_release(params[:id])
          return release if release

          render_error("forbidden", "owner role required", status: :forbidden)
          nil
        end

        def release_runtime_attributes
          Releases::RuntimeAttributes.new(
            params: {
              git_sha: params[:git_sha],
              image_repository: params[:image_repository],
              image_digest: params[:image_digest],
              revision: params[:revision],
              web: params[:web],
              worker: params[:worker],
              init: params[:init],
              release_command: params[:release_command],
              entrypoint: params[:entrypoint],
              command: params[:command],
              env_vars: params[:env_json].presence || params[:env_vars],
              secret_refs: params[:secret_refs_json].presence || params[:secret_refs],
              port: params[:port],
              healthcheck: params[:healthcheck],
              healthcheck_interval_seconds: params[:healthcheck_interval_seconds],
              healthcheck_timeout_seconds: params[:healthcheck_timeout_seconds]
            }
          ).to_h
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

        def publish_request_token
          token = params[:request_token].to_s.strip
          return token if token.present?

          SecureRandom.hex(16)
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
