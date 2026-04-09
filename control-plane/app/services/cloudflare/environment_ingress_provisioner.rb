# frozen_string_literal: true

require "securerandom"

module Cloudflare
  class EnvironmentIngressProvisioner
    def initialize(environment:, client: RestClient.new, secret_manager: Gcp::EnvironmentSecretManager.new,
      hostname_generator: nil, origin_service: Devopsellence::IngressConfig.envoy_origin)
      @environment = environment
      @client = client
      @secret_manager = secret_manager
      @hostname_generator = hostname_generator || -> { SecureRandom.alphanumeric(EnvironmentIngress::HOSTNAME_LENGTH).downcase }
      @origin_service = origin_service
    end

    def call
      ingress = environment.environment_ingress || environment.build_environment_ingress(status: EnvironmentIngress::STATUS_PENDING)
      if environment.environment_bundle&.hostname.present? && environment.environment_bundle&.cloudflare_tunnel_id.present?
        ingress.hostname = environment.environment_bundle.hostname
        ingress.cloudflare_tunnel_id = environment.environment_bundle.cloudflare_tunnel_id
        ingress.gcp_secret_name = environment.environment_bundle.gcp_secret_name
        ingress.provisioned_at ||= environment.environment_bundle.provisioned_at || Time.current
        ingress.save! if ingress.new_record? || ingress.changed?
      end

      hostname = ingress.hostname.presence || next_hostname!
      ingress.hostname = hostname
      ingress.gcp_secret_name ||= environment.environment_bundle&.gcp_secret_name || "env-#{environment.id}-ingress-cloudflare-tunnel-token"
      ingress.status = EnvironmentIngress::STATUS_PENDING
      ingress.save! if ingress.new_record? || ingress.changed?

      if Devopsellence::IngressConfig.local?
        ingress.cloudflare_tunnel_id = ingress.cloudflare_tunnel_id.presence || "local-env-#{environment.id}"
        ingress.status = EnvironmentIngress::STATUS_READY
        ingress.last_error = nil
        ingress.provisioned_at ||= Time.current
        ingress.save!
        return ingress
      end

      if ingress.hostname.present? && ingress.cloudflare_tunnel_id.present?
        ensure_managed_tunnel_routing!(ingress)
        mark_ingress_ready!(ingress)
        return ingress
      end

      tunnel = client.create_tunnel(name: tunnel_name(hostname))
      tunnel_token = client.tunnel_token(tunnel_id: tunnel.fetch("id"))

      ingress.cloudflare_tunnel_id = tunnel.fetch("id")
      ensure_managed_tunnel_routing!(ingress)
      mark_ingress_ready!(ingress, provisioned_at: Time.current)
      secret_manager.upsert_ingress_token!(environment_ingress: ingress, value: tunnel_token)
      ingress
    rescue StandardError => error
      ingress ||= environment.environment_ingress || environment.build_environment_ingress
      ingress.status = EnvironmentIngress::STATUS_FAILED
      ingress.last_error = error.message
      if ingress.persisted? && ingress.hostname.present? && ingress.gcp_secret_name.present?
        ingress.save!(validate: false)
      end
      raise
    end

    private

    attr_reader :environment, :client, :secret_manager, :hostname_generator, :origin_service

    def next_hostname!
      20.times do
        hostname = "#{hostname_generator.call}.#{zone_name}"
        return hostname unless EnvironmentIngress.exists?(hostname: hostname)
      end

      raise "failed to allocate a unique ingress hostname"
    end

    def tunnel_name(hostname)
      if environment.environment_bundle
        "envb-#{environment.environment_bundle.token}-#{hostname.split(".").first}"
      else
        "env-#{environment.id}-#{hostname.split(".").first}"
      end
    end

    def zone_name
      Devopsellence::IngressConfig.hostname_zone_name
    end

    def ensure_managed_tunnel_routing!(ingress)
      client.delete_dns_records(hostname: ingress.hostname, type: "A")
      client.delete_dns_records(hostname: ingress.hostname, type: "CNAME")
      client.configure_tunnel(
        tunnel_id: ingress.cloudflare_tunnel_id,
        hostname: ingress.hostname,
        service: origin_service
      )
      client.create_dns_cname(hostname: ingress.hostname, target: "#{ingress.cloudflare_tunnel_id}.cfargotunnel.com")
    end

    def mark_ingress_ready!(ingress, provisioned_at: ingress.provisioned_at || Time.current)
      ingress.status = EnvironmentIngress::STATUS_READY
      ingress.last_error = nil
      ingress.provisioned_at ||= provisioned_at
      ingress.save!
    end
  end
end
