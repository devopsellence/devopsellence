# frozen_string_literal: true

module Gcp
  class EnvironmentSecretManager
    def initialize(client: nil, broker: nil)
      @broker = broker || Runtime::Broker.current
    end

    def upsert!(environment_secret:, value:)
      broker.upsert_environment_secret!(environment_secret:, value:)
    end

    def upsert_ingress_token!(environment_ingress:, value:)
      broker.upsert_environment_ingress_secret!(environment_ingress:, value:)
    end

    def destroy!(environment_secret:)
      broker.destroy_environment_secret!(environment_secret:)
    end

    def ensure_environment_access!(environment_secret:)
      broker.ensure_environment_secret_access!(environment_secret:)
    end

    def ensure_ingress_access!(environment_ingress:)
      broker.ensure_environment_ingress_access!(environment_ingress:)
    end

    private

    attr_reader :broker
  end
end
