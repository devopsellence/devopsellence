# frozen_string_literal: true

module Api
  module V1
    module Cli
      class NodeDiagnoseRequestsController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!

        def create
          node = owned_node(params[:node_id])
          return render_error("forbidden", "owner role required", status: :forbidden) unless node

          request = NodeDiagnoseRequest.find_or_create_active_for(node: node, requested_by_user: current_user)
          signal_diagnose_request!(node) if request.previous_changes.key?("id")

          render json: serialize(request), status: :accepted
        end

        def show
          request = NodeDiagnoseRequest.joins(node: :organization)
            .where(organizations: { id: current_user.owned_organizations.select(:id) })
            .includes(:node)
            .find_by(id: params[:id])
          return render_error("forbidden", "owner role required", status: :forbidden) unless request

          render json: serialize(request)
        end

        private

          def owned_node(id)
            Node.joins(:organization)
              .where(organizations: { id: current_user.owned_organizations.select(:id) })
              .find_by(id: id)
          end

          def signal_diagnose_request!(node)
            Nodes::DiagnoseSignalPublisher.new(node: node).call
          end

          def serialize(request)
            {
              id: request.id,
              status: request.status,
              requested_at: request.requested_at&.utc&.iso8601,
              claimed_at: request.claimed_at&.utc&.iso8601,
              completed_at: request.completed_at&.utc&.iso8601,
              error_message: request.error_message,
              node: {
                id: request.node_id,
                name: request.node.name,
                organization_id: request.node.organization_id
              },
              result: request.result_payload
            }
          end
      end
    end
  end
end
