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

      if Devopsellence::IngressConfig.local?
        mark_ready!
      elsif addresses.any?
        cutover_to_direct_dns!(addresses)
        mark_ready!
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

    def mark_ready!
      ingress.update!(
        status: EnvironmentIngress::STATUS_READY,
        last_error: nil,
        provisioned_at: ingress.provisioned_at || Time.current
      )
    end

    def cutover_to_direct_dns!(addresses)
      snapshots = dns_snapshots
      stale_hosts.each do |host|
        client.delete_dns_records(hostname: host, type: "A")
        client.delete_dns_records(hostname: host, type: "CNAME")
      end
      ingress.hosts.each do |host|
        client.delete_dns_records(hostname: host, type: "CNAME")
        client.replace_dns_a_records(hostname: host, addresses:)
      end
    rescue StandardError
      restore_dns_snapshots(snapshots)
      raise
    end

    def dns_snapshots
      ingress.hosts.index_with do |host|
        Array(client.dns_records(hostname: host, type: "CNAME")) + Array(client.dns_records(hostname: host, type: "A"))
      end
    end

    def restore_dns_snapshots(snapshots)
      return if snapshots.blank?
      return unless client.respond_to?(:restore_dns_records)

      snapshots.each do |host, records|
        client.delete_dns_records(hostname: host, type: "A")
        client.delete_dns_records(hostname: host, type: "CNAME")
        client.restore_dns_records(records)
      end
    rescue StandardError
      nil
    end
  end
end
