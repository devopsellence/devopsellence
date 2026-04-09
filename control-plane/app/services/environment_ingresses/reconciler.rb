# frozen_string_literal: true

module EnvironmentIngresses
  class Reconciler
    def initialize(environment:, client: Cloudflare::RestClient.new)
      @environment = environment
      @client = client
    end

    def call
      ingress = ensure_ingress!
      return nil unless ingress

      if environment.direct_dns_ingress?
        DirectDnsStrategy.new(environment:, ingress:, client:).call
      else
        Cloudflare::EnvironmentIngressProvisioner.new(environment:, client:).call
      end
    end

    private

    attr_reader :environment, :client

    def ensure_ingress!
      ingress = environment.environment_ingress
      return ingress if ingress

      bundle = environment.environment_bundle
      if bundle&.hostname.present?
        return environment.create_environment_ingress!(
          hostname: bundle.hostname,
          cloudflare_tunnel_id: bundle.cloudflare_tunnel_id,
          gcp_secret_name: bundle.gcp_secret_name,
          status: environment.direct_dns_ingress? ? EnvironmentIngress::STATUS_PENDING : EnvironmentIngress::STATUS_READY,
          provisioned_at: bundle.provisioned_at || Time.current
        )
      end

      Cloudflare::EnvironmentIngressProvisioner.new(environment:, client:).call
    end
  end
end
