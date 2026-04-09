# frozen_string_literal: true

module Api
  module V1
    module Agent
      class DesiredStatesController < Api::V1::Agent::BaseController
        before_action :authenticate_node_access!

        def show
          document = StandaloneDesiredStateDocument.find_by(
            node_id: current_node.id,
            sequence: current_node.desired_state_sequence
          )
          return render_error("not_found", "desired state not found", status: :not_found) unless document

          quoted_etag = %("#{document.etag}")
          if normalize_etag(request.headers["If-None-Match"]) == document.etag
            response.set_header("ETag", quoted_etag)
            return head :not_modified
          end

          response.set_header("ETag", quoted_etag)
          response.set_header("Cache-Control", "private, max-age=0")
          render json: JSON.parse(document.payload_json)
        end

        private

        def normalize_etag(value)
          value.to_s.delete_prefix("W/").delete_prefix('"').delete_suffix('"').presence
        end
      end
    end
  end
end
