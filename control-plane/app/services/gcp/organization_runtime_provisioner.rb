# frozen_string_literal: true

module Gcp
  class OrganizationRuntimeProvisioner
    Result = Struct.new(:status, :message, keyword_init: true)

    def initialize(organization:, client: nil, retry_sleep_seconds: 1, broker: nil)
      @organization = organization
      @broker = broker || Runtime::Broker.current
    end

    def call
      OrganizationBundles::Claim.new(organization:, broker:).call
      Result.new(status: Organization::PROVISIONING_READY, message: nil)
    rescue OrganizationBundles::Claim::Error => error
      Result.new(status: Organization::PROVISIONING_FAILED, message: error.message)
    end

    private

    attr_reader :organization, :broker
  end
end
