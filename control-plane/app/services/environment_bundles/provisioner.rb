# frozen_string_literal: true

require "securerandom"

module EnvironmentBundles
  class Provisioner
    Error = Class.new(StandardError)

    def initialize(organization_bundle:, broker: nil)
      @organization_bundle = organization_bundle
      @broker = broker || Runtime::Broker.current
    end

    def call
      Rails.logger.info("[environment_bundles/provisioner] creating environment bundle organization_bundle=#{organization_bundle.token}")
      bundle = EnvironmentBundle.create!(
        runtime_project: organization_bundle.runtime_project,
        organization_bundle: organization_bundle
      )

      Rails.logger.info("[environment_bundles/provisioner] provisioning GCP service account bundle=#{bundle.token}")
      result = broker.provision_environment_bundle!(bundle:)
      raise Error, result.message unless result.status == :ready

      hostname = allocate_hostname!
      bundle.update!(hostname:)
      Rails.logger.info("[environment_bundles/provisioner] hostname allocated bundle=#{bundle.token} hostname=#{hostname}")

      bundle.update!(status: EnvironmentBundle::STATUS_WARM, provisioned_at: Time.current, provisioning_error: nil)
      Rails.logger.info("[environment_bundles/provisioner] environment bundle warm bundle=#{bundle.token}")
      bundle
    rescue StandardError => error
      bundle&.update!(status: EnvironmentBundle::STATUS_FAILED, provisioning_error: error.message) rescue nil
      Rails.logger.error("[environment_bundles/provisioner] provisioning failed bundle=#{bundle&.token} error=#{error.message}")
      raise Error, error.message
    end

    private

    attr_reader :organization_bundle, :broker

    def allocate_hostname!
      next_hostname!
    end

    def next_hostname!
      zone = hostname_zone_name
      20.times do
        candidate = "#{SecureRandom.alphanumeric(EnvironmentIngress::HOSTNAME_LENGTH).downcase}.#{zone}"
        return candidate unless EnvironmentIngressHost.exists?(hostname: candidate) ||
          EnvironmentIngress.exists?(hostname: candidate) ||
          EnvironmentBundle.exists?(hostname: candidate)
      end
      raise "failed to allocate a unique bundle ingress hostname"
    end

    def hostname_zone_name
      Devopsellence::IngressConfig.hostname_zone_name
    end
  end
end
