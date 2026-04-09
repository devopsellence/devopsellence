# frozen_string_literal: true

module Runtime
  module Broker
    module_function

    def current
      runtime = Devopsellence::RuntimeConfig.current
      if @current.nil? || current_backend != runtime.runtime_backend
        @current = build_current(runtime:)
      end

      @current
    end

    def reset_current!
      @current = nil
    end

    def build_current(runtime: Devopsellence::RuntimeConfig.current)
      return StandaloneClient.new if runtime.runtime_backend == RuntimeProject::BACKEND_STANDALONE

      LocalClient.new
    end

    def current_backend
      return RuntimeProject::BACKEND_STANDALONE if @current.is_a?(StandaloneClient)

      RuntimeProject::BACKEND_GCP
    end
  end
end
