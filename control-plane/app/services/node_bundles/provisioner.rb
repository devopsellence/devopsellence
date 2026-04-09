# frozen_string_literal: true

module NodeBundles
  class Provisioner
    Error = Class.new(StandardError)

    def initialize(environment_bundle:, broker: nil)
      @environment_bundle = environment_bundle
      @broker = broker || Runtime::Broker.current
    end

    def call
      Rails.logger.info("[node_bundles/provisioner] creating node bundle environment_bundle=#{environment_bundle.token}")
      bundle = NodeBundle.create!(
        runtime_project: environment_bundle.runtime_project,
        organization_bundle: environment_bundle.organization_bundle,
        environment_bundle: environment_bundle
      )

      Rails.logger.info("[node_bundles/provisioner] setting up impersonation bundle=#{bundle.token}")
      setup_impersonation!(bundle)

      unless environment_bundle.runtime_project&.standalone?
        Rails.logger.info("[node_bundles/provisioner] verifying GCP readiness bundle=#{bundle.token}")
        verify_readiness!(bundle)
      end

      bundle.update!(status: NodeBundle::STATUS_WARM, provisioned_at: Time.current, provisioning_error: nil)
      Rails.logger.info("[node_bundles/provisioner] node bundle warm bundle=#{bundle.token}")
      bundle
    rescue StandardError => error
      bundle&.update!(status: NodeBundle::STATUS_FAILED, provisioning_error: error.message) rescue nil
      Rails.logger.error("[node_bundles/provisioner] provisioning failed bundle=#{bundle&.token} error=#{error.message}")
      raise Error, error.message
    end

    private

    attr_reader :environment_bundle, :broker

    def setup_impersonation!(bundle)
      result = broker.ensure_node_bundle_impersonation!(bundle:)
      raise Error, result.message unless result.status == :ready
    end

    def verify_readiness!(bundle)
      readiness = Gcp::NodeBundleReadiness.new(
        node_bundle: bundle,
        issuer: Devopsellence::RuntimeConfig.current.public_base_url
      ).call
      raise Error, readiness.message unless readiness.status == :ready
    end
  end
end
