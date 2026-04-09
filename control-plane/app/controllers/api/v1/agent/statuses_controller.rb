# frozen_string_literal: true

module Api
  module V1
    module Agent
      class StatusesController < Api::V1::Agent::BaseController
        before_action :authenticate_node_access!

        def create
          result = Deployments::ProgressRecorder.new(node: current_node, status: status_params).call

          render json: {
            tracked: result.tracked,
            deployment_id: result.deployment_node_status&.deployment_id,
            phase: result.deployment_node_status&.phase
          }, status: :accepted
        end

        private

        def status_params
          payload = params[:status].presence || params
          permitted = payload.permit(
            :time,
            :revision,
            :phase,
            :message,
            :error,
            task: [ :name, :phase, :message, :error, :exit_code ],
            containers: [ :name, :state, :hash ],
            ingress: [ :tls_status, :tls_not_after, :tls_error ]
          )
          permitted.to_h.deep_symbolize_keys
        end
      end
    end
  end
end
