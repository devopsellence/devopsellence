# frozen_string_literal: true

module Api
  module V1
    module Cli
      class NodeAssignmentsController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!

        def destroy
          node = Node.joins(:organization)
            .where(organizations: { id: current_user.owned_organizations.select(:id) })
            .find_by(id: params[:node_id])
          return render_error("forbidden", "owner role required", status: :forbidden) unless node
          return render_error("forbidden", "manual node management is unavailable for trial organizations", status: :forbidden) if node.organization&.trial?

          result = if node.managed?
            Nodes::Cleanup.new(node: node).call
          else
            Nodes::Unassign.new(node: node).call
          end

          render json: {
            id: node.id,
            organization_id: node.organization_id,
            environment_id: result.environment&.id,
            desired_state_uri: node.desired_state_uri,
            managed: node.managed,
            revoked_at: node.revoked_at&.utc&.iso8601
          }
        end
      end
    end
  end
end
