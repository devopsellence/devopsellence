# frozen_string_literal: true

module OrganizationBundles
  class Provisioner
    Error = Class.new(StandardError)

    def initialize(runtime_project:, broker: nil)
      @runtime_project = runtime_project
      @broker = broker || Runtime::Broker.current
    end

    def call
      Rails.logger.info("[organization_bundles/provisioner] creating organization bundle runtime_project=#{runtime_project.slug}")
      bundle = OrganizationBundle.create!(runtime_project: runtime_project)

      Rails.logger.info("[organization_bundles/provisioner] provisioning GCP resources bundle=#{bundle.token}")
      result = broker.provision_organization_bundle!(bundle:)
      raise Error, result.message unless result.status == :ready

      bundle.update!(status: OrganizationBundle::STATUS_WARM, provisioned_at: Time.current, provisioning_error: nil)
      Rails.logger.info("[organization_bundles/provisioner] organization bundle warm bundle=#{bundle.token}")
      bundle
    rescue StandardError => error
      bundle&.update!(status: OrganizationBundle::STATUS_FAILED, provisioning_error: error.message) rescue nil
      Rails.logger.error("[organization_bundles/provisioner] provisioning failed bundle=#{bundle&.token} error=#{error.message}")
      raise Error, error.message
    end

    private

    attr_reader :runtime_project, :broker
  end
end
