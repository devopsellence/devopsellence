# frozen_string_literal: true

require "securerandom"

module EnvironmentBundles
  class Provisioner
    Error = Class.new(StandardError)

    def initialize(organization_bundle:, broker: nil, cloudflare_client: nil)
      @organization_bundle = organization_bundle
      @broker = broker || Runtime::Broker.current
      @cloudflare_client = cloudflare_client || Cloudflare::RestClient.new
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

      Rails.logger.info("[environment_bundles/provisioner] provisioning Cloudflare tunnel bundle=#{bundle.token}")
      hostname, tunnel_id, tunnel_token = provision_cloudflare_tunnel!(bundle)
      bundle.update!(hostname:, cloudflare_tunnel_id: tunnel_id)
      Rails.logger.info("[environment_bundles/provisioner] tunnel created bundle=#{bundle.token} hostname=#{hostname} tunnel_id=#{tunnel_id}")

      result = broker.upsert_environment_bundle_tunnel_secret!(bundle:, tunnel_token:)
      raise Error, result.message unless result.status == :ready

      bundle.update!(status: EnvironmentBundle::STATUS_WARM, provisioned_at: Time.current, provisioning_error: nil)
      Rails.logger.info("[environment_bundles/provisioner] environment bundle warm bundle=#{bundle.token}")
      bundle
    rescue StandardError => error
      bundle&.update!(status: EnvironmentBundle::STATUS_FAILED, provisioning_error: error.message) rescue nil
      Rails.logger.error("[environment_bundles/provisioner] provisioning failed bundle=#{bundle&.token} error=#{error.message}")
      raise Error, error.message
    end

    private

    attr_reader :organization_bundle, :broker, :cloudflare_client

    def provision_cloudflare_tunnel!(bundle)
      if Devopsellence::IngressConfig.local?
        hostname = next_hostname!
        tunnel_id = "local-#{bundle.token}"
        return [ hostname, tunnel_id, tunnel_id ]
      end

      hostname = next_hostname!
      tunnel = cloudflare_client.create_tunnel(name: "envb-#{bundle.token}")
      tunnel_id = tunnel.fetch("id")
      tunnel_token = cloudflare_client.tunnel_token(tunnel_id: tunnel_id)

      cloudflare_client.configure_tunnel(
        tunnel_id: tunnel_id,
        hostname: hostname,
        service: origin_service
      )
      cloudflare_client.create_dns_cname(hostname: hostname, target: "#{tunnel_id}.cfargotunnel.com")

      [ hostname, tunnel_id, tunnel_token ]
    end

    def next_hostname!
      zone = hostname_zone_name
      20.times do
        candidate = "#{SecureRandom.alphanumeric(EnvironmentIngress::HOSTNAME_LENGTH).downcase}.#{zone}"
        return candidate unless EnvironmentIngress.exists?(hostname: candidate) || EnvironmentBundle.exists?(hostname: candidate)
      end
      raise "failed to allocate a unique bundle ingress hostname"
    end

    def origin_service
      Devopsellence::IngressConfig.envoy_origin
    end

    def hostname_zone_name
      Devopsellence::IngressConfig.hostname_zone_name
    end
  end
end
