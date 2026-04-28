# frozen_string_literal: true

module Gcp
  class EnvironmentSecretManager
    def initialize(client: nil, broker: nil)
      @broker = broker || Runtime::Broker.current
    end

    def upsert!(environment_secret:, value:)
      broker.upsert_environment_secret!(environment_secret:, value:)
    end

    def destroy!(environment_secret:)
      broker.destroy_environment_secret!(environment_secret:)
    end

    def ensure_environment_access!(environment_secret:)
      broker.ensure_environment_secret_access!(environment_secret:)
    end

    private

    attr_reader :broker
  end
end
