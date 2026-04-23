# frozen_string_literal: true

require "securerandom"

module Cloudflare
  class EnvironmentIngressProvisioner
    def initialize(environment:, client: RestClient.new, secret_manager: Gcp::EnvironmentSecretManager.new,
      hostname_generator: nil, origin_service: Devopsellence::IngressConfig.envoy_origin, release: nil, stale_hosts: [])
      @environment = environment
      @client = client
      @secret_manager = secret_manager
      @hostname_generator = hostname_generator || -> { SecureRandom.alphanumeric(EnvironmentIngress::HOSTNAME_LENGTH).downcase }
      @origin_service = origin_service
      @release = release || environment.current_release
      @stale_hosts = stale_hosts
    end

    def call
      ingress = environment.environment_ingress || environment.build_environment_ingress(status: EnvironmentIngress::STATUS_PENDING)
      previous_hosts = ingress.hosts
      if environment.environment_bundle&.hostname.present? && environment.environment_bundle&.cloudflare_tunnel_id.present?
        ingress.cloudflare_tunnel_id = environment.environment_bundle.cloudflare_tunnel_id
        ingress.gcp_secret_name = environment.environment_bundle.gcp_secret_name
        ingress.provisioned_at ||= environment.environment_bundle.provisioned_at || Time.current
      end

      hosts = desired_hosts_for(ingress)
      ingress.hostname = hosts.first
      ingress.gcp_secret_name ||= environment.environment_bundle&.gcp_secret_name || "env-#{environment.id}-ingress-cloudflare-tunnel-token"
      ingress.status = EnvironmentIngress::STATUS_PENDING
      ingress.save! if ingress.new_record? || ingress.changed?
      ingress.assign_hosts!(hosts) if hosts != ingress.hosts

      if Devopsellence::IngressConfig.local?
        ingress.cloudflare_tunnel_id = ingress.cloudflare_tunnel_id.presence || "local-env-#{environment.id}"
        ingress.status = EnvironmentIngress::STATUS_READY
        ingress.last_error = nil
        ingress.provisioned_at ||= Time.current
        ingress.save!
        return ingress
      end

      if ingress.primary_hostname.present? && ingress.cloudflare_tunnel_id.present?
        ensure_managed_tunnel_routing!(ingress, stale_hosts: removed_hosts(previous_hosts, ingress))
        mark_ingress_ready!(ingress)
        return ingress
      end

      tunnel = client.create_tunnel(name: tunnel_name(ingress.primary_hostname))
      tunnel_token = client.tunnel_token(tunnel_id: tunnel.fetch("id"))

      ingress.cloudflare_tunnel_id = tunnel.fetch("id")
      ensure_managed_tunnel_routing!(ingress, stale_hosts: removed_hosts(previous_hosts, ingress))
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

    attr_reader :environment, :client, :secret_manager, :hostname_generator, :origin_service, :release, :stale_hosts

    def next_hostname!
      20.times do
        hostname = "#{hostname_generator.call}.#{zone_name}"
        return hostname unless EnvironmentIngressHost.exists?(hostname: hostname) || EnvironmentIngress.exists?(hostname: hostname)
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

    def ensure_managed_tunnel_routing!(ingress, stale_hosts: [])
      stale_hosts.each do |host|
        client.delete_dns_records(hostname: host, type: "A")
        client.delete_dns_records(hostname: host, type: "CNAME")
      end
      ingress.hosts.each do |host|
        client.delete_dns_records(hostname: host, type: "A")
        client.delete_dns_records(hostname: host, type: "CNAME")
      end
      client.configure_tunnel(
        tunnel_id: ingress.cloudflare_tunnel_id,
        hostnames: ingress.hosts,
        service: origin_service
      )
      ingress.hosts.each do |host|
        client.create_dns_cname(hostname: host, target: "#{ingress.cloudflare_tunnel_id}.cfargotunnel.com")
      end
    end

    def mark_ingress_ready!(ingress, provisioned_at: ingress.provisioned_at || Time.current)
      ingress.status = EnvironmentIngress::STATUS_READY
      ingress.last_error = nil
      ingress.provisioned_at ||= provisioned_at
      ingress.save!
    end

    def desired_hosts_for(ingress)
      configured = Array(release&.ingress_config&.dig("hosts")).map(&:to_s).map(&:strip).reject(&:blank?).uniq
      return configured if configured.any?
      return ingress.hosts if ingress.hosts.any?
      return [ environment.environment_bundle.hostname ] if environment.environment_bundle&.hostname.present?

      [ next_hostname! ]
    end

    def removed_hosts(previous_hosts, ingress)
      ((previous_hosts - ingress.hosts) | stale_hosts).uniq
    end
  end
end
