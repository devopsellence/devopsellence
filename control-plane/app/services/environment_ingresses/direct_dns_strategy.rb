# frozen_string_literal: true

module EnvironmentIngresses
  class DirectDnsStrategy
    def initialize(environment:, ingress:, client: Cloudflare::RestClient.new)
      @environment = environment
      @ingress = ingress
      @client = client
    end

    def call
      raise "environment ingress hostname is required" if ingress.hostname.blank?

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
          last_error: "no eligible direct_dns web nodes with fresh heartbeat, settled rollout, and ready TLS"
        )
      end

      ingress
    rescue StandardError => error
      ingress.update!(status: EnvironmentIngress::STATUS_FAILED, last_error: error.message) if ingress.persisted?
      raise
    end

    private

    attr_reader :environment, :ingress, :client

    def cutover_to_direct_dns!(addresses)
      client.delete_dns_records(hostname: ingress.hostname, type: "CNAME")
      client.replace_dns_a_records(hostname: ingress.hostname, addresses:)
    rescue StandardError
      restore_tunnel_routing!
      raise
    end

    def restore_tunnel_routing!
      return if ingress.cloudflare_tunnel_id.to_s.strip.empty?

      client.delete_dns_records(hostname: ingress.hostname, type: "A")
      client.create_dns_cname(
        hostname: ingress.hostname,
        target: "#{ingress.cloudflare_tunnel_id}.cfargotunnel.com"
      )
    rescue StandardError
      nil
    end
  end
end
