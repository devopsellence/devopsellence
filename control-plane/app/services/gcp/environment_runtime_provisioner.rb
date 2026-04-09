# frozen_string_literal: true

module Gcp
  class EnvironmentRuntimeProvisioner
    Result = Struct.new(:status, :message, keyword_init: true)

    def initialize(environment:, client: nil, iam: nil, retry_sleep_seconds: 1, broker: nil)
      @environment = environment
      @broker = broker || Runtime::Broker.current
    end

    def call
      EnvironmentBundles::Claim.new(environment:, broker:).call
      Result.new(status: :ready, message: nil)
    rescue EnvironmentBundles::Claim::Error => error
      Result.new(status: :failed, message: error.message)
    end

    private

    attr_reader :environment, :broker
  end
end
