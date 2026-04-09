# frozen_string_literal: true

module Api
  module V1
    class BaseController < ActionController::API
      private

      def render_error(code, description, status: :bad_request)
        render json: { error: code, error_description: description }, status: status
      end
    end
  end
end
