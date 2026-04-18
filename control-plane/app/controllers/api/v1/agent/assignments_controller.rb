# frozen_string_literal: true

module Api
  module V1
    module Agent
      class AssignmentsController < Api::V1::Agent::BaseController
        before_action :authenticate_node_access!

        def show
          target = Nodes::DesiredStateTarget.for(node: current_node, capabilities: current_agent_capabilities)

          if target
            render json: target
          else
            envelope = Nodes::DesiredStateEnvelope.wrap(
              node: current_node,
              environment: nil,
              sequence: current_node.desired_state_sequence,
              payload: { schemaVersion: 2, revision: "unassigned", environments: [] }
            )
            render json: { mode: "unassigned", desired_state: envelope }
          end
        end
      end
    end
  end
end
