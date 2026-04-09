# frozen_string_literal: true

module Api
  module V1
    module Agent
      class DiagnoseRequestsController < Api::V1::Agent::BaseController
        before_action :authenticate_node_access!

        def claim
          request = NodeDiagnoseRequest.claim_pending_for(node: current_node)
          return head :no_content unless request

          render json: {
            id: request.id,
            requested_at: request.requested_at&.utc&.iso8601
          }
        end

        def create_result
          request = current_node.node_diagnose_requests.find_by(id: params[:id])
          return render_error("not_found", "diagnose request not found", status: :not_found) unless request
          unless request.claimed?
            if request.pending?
              return render_error("invalid_request", "diagnose request must be claimed first", status: :unprocessable_entity)
            else
              return render_error("invalid_request", "diagnose request already finished", status: :unprocessable_entity)
            end
          end

          error_message = params[:error].to_s.strip
          if error_message.present?
            request.fail!(message: error_message)
          else
            result = diagnose_result_payload
            return render_error("invalid_request", "missing result payload", status: :unprocessable_entity) unless result

            request.complete!(result: result)
          end

          render json: {
            id: request.id,
            status: request.status,
            completed_at: request.completed_at&.utc&.iso8601
          }, status: :accepted
        end

        private

          def diagnose_result_payload
            payload = params[:result]
            return nil unless payload.present?

            case payload
            when ActionController::Parameters
              payload.to_unsafe_h
            when Hash
              payload
            else
              nil
            end
          end
      end
    end
  end
end
