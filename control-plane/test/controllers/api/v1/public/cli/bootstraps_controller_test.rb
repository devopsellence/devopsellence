# frozen_string_literal: true

require "test_helper"

module Api
  module V1
    module Public
      module Cli
        class BootstrapsControllerTest < ActiveSupport::TestCase
          test "bootstrap rate limit has explicit name" do
            limits = BootstrapsController._process_action_callbacks
              .select { |callback| callback.kind == :before && callback.filter.respond_to?(:binding) }
              .map { |callback| callback.filter.binding.local_variable_get(:name) }

            assert_includes limits, "public_cli_bootstrap"
          end

          test "bootstrap rate limit keys by remote ip" do
            by_proc = BootstrapsController._process_action_callbacks
              .select { |callback| callback.kind == :before && callback.filter.respond_to?(:binding) }
              .map { |callback| callback.filter.binding.local_variable_get(:by) }
              .first

            controller = BootstrapsController.new
            request = ActionDispatch::TestRequest.create("REMOTE_ADDR" => "198.51.100.25")
            controller.set_request!(request)

            assert_equal "198.51.100.25", controller.instance_exec(&by_proc)
          end
        end
      end
    end
  end
end
