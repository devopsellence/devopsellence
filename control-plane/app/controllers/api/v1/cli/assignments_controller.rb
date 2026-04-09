# frozen_string_literal: true

module Api
  module V1
    module Cli
      class AssignmentsController < Api::V1::Cli::BaseController
        include ActionController::Live

        before_action :authenticate_cli_access!

        def create
          environment = find_owned_environment
          return unless environment

          if environment.project.organization.trial?
            return render_error("forbidden", "manual node management is unavailable for trial organizations", status: :forbidden)
          end

          node = environment.project.organization.nodes.find_by(id: params[:node_id])
          return render_error("not_found", "node not found", status: :not_found) unless node
          if node.revoked_at.present?
            return render_error(
              "invalid_request",
              "node has been deleted; bootstrap again to reuse this machine",
              status: :unprocessable_entity
            )
          end

          response.headers["Content-Type"] = "text/event-stream"
          response.headers["Cache-Control"] = "no-cache"
          sse = ActionController::Live::SSE.new(response.stream, retry: 300)

          on_progress = ->(message) { sse.write({ message: message }, event: "progress") }

          result = Nodes::AssignmentManager.new(
            node: node,
            environment: environment,
            issuer: PublicBaseUrl.resolve(request),
            on_progress: on_progress
          ).call

          if environment.current_release&.requires_label?(Node::LABEL_WEB) && node.labeled?(Node::LABEL_WEB)
            sse.write({ message: "Configuring Cloudflare ingress..." }, event: "progress")
            Cloudflare::EnvironmentIngressProvisioner.new(environment: environment).call
          end

          node.reload
          sse.write({
            node_id: node.id,
            environment_id: environment.id,
            sequence: node.desired_state_sequence,
            desired_state_bucket: node.desired_state_bucket,
            desired_state_object_path: node.desired_state_object_path,
            desired_state_uri: node.desired_state_uri
          }, event: "complete")
        rescue => error
          sse&.write({ message: error.message }, event: "error")
        ensure
          sse&.close
        end

        private

        def find_environment
          environment = member_environment(params[:environment_id])
          return environment if environment

          render_error("not_found", "environment not found", status: :not_found)
          nil
        end

        def find_owned_environment
          environment = owner_environment(params[:environment_id])
          return environment if environment

          render_error("forbidden", "owner role required", status: :forbidden)
          nil
        end
      end
    end
  end
end
