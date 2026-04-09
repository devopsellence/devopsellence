# frozen_string_literal: true

require "test_helper"

module Api
  module V1
    module Cli
      class AuthControllerTest < ActiveSupport::TestCase
        test "rate limits use distinct names" do
          limits = AuthController._process_action_callbacks
            .select { |callback| callback.kind == :before && callback.filter.respond_to?(:binding) }
            .map { |callback| callback.filter.binding.local_variable_get(:name) }

          assert_includes limits, "auth_start"
          assert_includes limits, "auth_token"
          assert_equal 2, limits.compact.uniq.size
        end

        test "rate limits key by remote ip" do
          by_procs = AuthController._process_action_callbacks
            .select { |callback| callback.kind == :before && callback.filter.respond_to?(:binding) }
            .map { |callback| callback.filter.binding.local_variable_get(:by) }

          controller = AuthController.new
          request = ActionDispatch::TestRequest.create("REMOTE_ADDR" => "198.51.100.24")
          controller.set_request!(request)

          assert_equal [ "198.51.100.24", "198.51.100.24" ], by_procs.map { |by| controller.instance_exec(&by) }
        end
      end
    end
  end
end
