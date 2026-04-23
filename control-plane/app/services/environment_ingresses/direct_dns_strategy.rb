# frozen_string_literal: true

module EnvironmentIngresses
  class DirectDnsStrategy
    def initialize(environment:, ingress:, client: Cloudflare::RestClient.new, stale_hosts: [])
      @environment = environment
      @ingress = ingress
      @client = client
      @stale_hosts = stale_hosts
    end

    def call
      raise "environment ingress hosts are required" if ingress.hosts.empty?

      addresses = EligibleNodes.new(environment:).call.map(&:public_ip).uniq.sort

      if addresses.any?
        cutover_to_direct_dns!(addresses)
        ingress.update!(
          status: EnvironmentIngress::STATUS_READY,
          last_error: nil,
          provisioned_at: ingress.provisioned_at || Time.current
        )
      else
        ingress.update!(
          status: EnvironmentIngress::STATUS_DEGRADED,
          last_error: "no eligible public web nodes with fresh heartbeat and settled rollout"
        )
      end

      ingress
    rescue StandardError => error
      ingress.update!(status: EnvironmentIngress::STATUS_FAILED, last_error: error.message) if ingress.persisted?
      raise
    end

    private

    attr_reader :environment, :ingress, :client, :stale_hosts

    def cutover_to_direct_dns!(addresses)
      stale_hosts.each do |host|
        client.delete_dns_records(hostname: host, type: "A")
        client.delete_dns_records(hostname: host, type: "CNAME")
      end
      ingress.hosts.each do |host|
        client.delete_dns_records(hostname: host, type: "CNAME")
        client.replace_dns_a_records(hostname: host, addresses:)
      end
    rescue StandardError
      restore_tunnel_routing!
      raise
    end

    def restore_tunnel_routing!
      return if ingress.cloudflare_tunnel_id.to_s.strip.empty?

      ingress.hosts.each do |host|
        client.delete_dns_records(hostname: host, type: "A")
        client.create_dns_cname(
          hostname: host,
          target: "#{ingress.cloudflare_tunnel_id}.cfargotunnel.com"
        )
      end
    rescue StandardError
      nil
    end
  end
end
