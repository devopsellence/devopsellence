# frozen_string_literal: true

module Cli
  class IngressStatusSerializer
    def initialize(environment:, ingress:, release: nil)
      @environment = environment
      @ingress = ingress
      @release = release || environment.current_release
    end

    def as_json
      return nil unless ingress

      payload = {
        hostname: ingress.primary_hostname,
        hosts: ingress.hosts,
        public_urls: verified_public_urls,
        configured_public_urls: configured_public_urls,
        status: ingress.status,
        last_error: ingress.last_error
      }.compact
      payload[:public_url] = verified_public_urls.first if verified_public_urls.any?
      payload[:public_url_status] = public_url_status if verified_public_urls.empty? && configured_public_urls.any?
      payload[:tls_status] = tls_status if tls_required? && tls_status.present?
      payload[:tls_error] = tls_error if tls_required? && tls_error.present?
      payload
    end

    private

      attr_reader :environment, :ingress, :release

      def verified_public_urls
        @verified_public_urls ||= begin
          if ingress.ready? && (!tls_required? || tls_ready?)
            configured_public_urls
          else
            []
          end
        end
      end

      def configured_public_urls
        @configured_public_urls ||= ingress.hosts.map { |host| public_url_for(host) }.compact
      end

      def public_url_status
        return "configured_tls_failed" if tls_required? && tls_status == Node::INGRESS_TLS_FAILED
        return "configured_tls_pending" if tls_required?

        "configured_pending"
      end

      def public_url_for(host)
        return Devopsellence::IngressConfig.public_url(host) if Devopsellence::IngressConfig.local?
        return nil if host.blank?

        "#{url_scheme}://#{host}"
      end

      def url_scheme
        tls_required? ? "https" : "http"
      end

      def tls_required?
        return false if Devopsellence::IngressConfig.local?

        mode = release&.ingress_config&.dig("tls", "mode").to_s.strip
        mode = "auto" if mode.blank?
        mode == "auto" || mode == "manual"
      end

      def tls_ready?
        tls_status == Node::INGRESS_TLS_READY
      end

      def tls_status
        @tls_status ||= begin
          nodes = eligible_ingress_nodes
          if nodes.empty?
            nil
          elsif nodes.any? { |node| node.ingress_tls_status == Node::INGRESS_TLS_FAILED }
            Node::INGRESS_TLS_FAILED
          elsif nodes.all?(&:ingress_tls_ready?)
            Node::INGRESS_TLS_READY
          else
            Node::INGRESS_TLS_PENDING
          end
        end
      end

      def tls_error
        eligible_ingress_nodes.filter_map(&:ingress_tls_last_error).first
      end

      def eligible_ingress_nodes
        @eligible_ingress_nodes ||= EnvironmentIngresses::EligibleNodes.new(environment:).call
      end
  end
end
