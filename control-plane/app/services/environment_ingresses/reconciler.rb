# frozen_string_literal: true

module EnvironmentIngresses
  class Reconciler
    def initialize(environment:, client: Cloudflare::RestClient.new, release: nil)
      @environment = environment
      @client = client
      @release = release || environment.current_release
    end

    def call
      ingress, stale_hosts = ensure_ingress!
      return nil unless ingress

      if environment.direct_dns_ingress?
        DirectDnsStrategy.new(environment:, ingress:, client:, stale_hosts:).call
      else
        Cloudflare::EnvironmentIngressProvisioner.new(environment:, client:, release:, stale_hosts:).call
      end
    end

    private

    attr_reader :environment, :client, :release

    def ensure_ingress!
      ingress = environment.environment_ingress
      previous_hosts = ingress&.hosts || []
      return [ sync_ingress_hosts!(ingress), previous_hosts - ingress.hosts ] if ingress

      bundle = environment.environment_bundle
      if bundle&.hostname.present?
        ingress = environment.create_environment_ingress!(
          hostname: bundle.hostname,
          cloudflare_tunnel_id: bundle.cloudflare_tunnel_id,
          gcp_secret_name: bundle.gcp_secret_name,
          status: environment.direct_dns_ingress? ? EnvironmentIngress::STATUS_PENDING : EnvironmentIngress::STATUS_READY,
          provisioned_at: bundle.provisioned_at || Time.current
        )
        return [ sync_ingress_hosts!(ingress), [] ]
      end

      ingress = Cloudflare::EnvironmentIngressProvisioner.new(environment:, client:, release:).call
      [ ingress, [] ]
    end

    def sync_ingress_hosts!(ingress)
      return ingress unless ingress

      desired_hosts = desired_hosts_for(ingress)
      ingress.assign_hosts!(desired_hosts) if desired_hosts.any? && desired_hosts != ingress.hosts
      ingress
    end

    def desired_hosts_for(ingress)
      desired_hosts = []
      if environment.environment_bundle&.hostname.present?
        desired_hosts << environment.environment_bundle.hostname
      end

      configured = IngressHostnames.normalize_all(release&.ingress_config&.dig("hosts"))
      desired_hosts.concat(configured)
      desired_hosts = desired_hosts.uniq
      return desired_hosts if desired_hosts.any?
      return ingress.hosts if ingress.hosts.any?
      return [ ingress.hostname ] if ingress.hostname.present?

      []
    end
  end
end
