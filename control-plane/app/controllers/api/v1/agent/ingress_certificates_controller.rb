# frozen_string_literal: true

require "time"

module Api
  module V1
    module Agent
      class IngressCertificatesController < Api::V1::Agent::BaseController
        before_action :authenticate_node_access!

        def create
          return render_error("forbidden", "direct_dns ingress is disabled for this environment", status: :forbidden) unless current_environment&.direct_dns_ingress?
          ingress_service_names = current_environment&.current_release&.ingress_target_service_names.to_a
          return render_error("forbidden", "node is not eligible for ingress", status: :forbidden) unless ingress_service_names.any? && current_environment.current_release.ingress_scheduled_on?(current_node)
          return render_error("forbidden", "node capability missing", status: :forbidden) unless current_node.supports_capability?(Node::CAPABILITY_DIRECT_DNS_INGRESS)

          ingress = current_environment.environment_ingress
          return render_error("invalid_request", "environment ingress is not provisioned", status: :unprocessable_entity) unless ingress
          return render_error("invalid_request", "hostname mismatch", status: :unprocessable_entity) unless ingress.hostname_matches?(hostname)

          result = nil
          ingress.with_lock do
            result = IngressCertificates::Issuer.new(hostname:, csr_pem: csr_pem).call
          end

          current_node.update!(
            ingress_tls_status: Node::INGRESS_TLS_READY,
            ingress_tls_not_after: result.not_after,
            ingress_tls_last_error: nil
          )
          EnvironmentIngresses::ReconcileJob.perform_later(current_environment.id)

          render json: {
            hostname: hostname,
            certificate_pem: result.certificate_pem,
            not_after: result.not_after.utc.iso8601
          }, status: :created
        rescue OpenSSL::OpenSSLError, ArgumentError => error
          mark_tls_failed!(error.message)
          render_error("invalid_request", error.message, status: :unprocessable_entity)
        rescue StandardError => error
          mark_tls_failed!(error.message)
          apply_retry_after_header!(error.message)
          render_error(error_code_for(error.message), error.message, status: error_status_for(error.message))
        end

        private

        def current_environment
          current_node.environment
        end

        def hostname
          params[:hostname].to_s.strip
        end

        def csr_pem
          params[:csr].to_s
        end

        def mark_tls_failed!(message)
          current_node.update_columns(
            ingress_tls_status: Node::INGRESS_TLS_FAILED,
            ingress_tls_last_error: message
          )
        rescue StandardError
          nil
        end

        def apply_retry_after_header!(message)
          retry_after = parsed_retry_after(message)
          return unless retry_after

          seconds = (retry_after - Time.current).ceil
          return unless seconds.positive?

          response.set_header("Retry-After", seconds.to_s)
        end

        def parsed_retry_after(message)
          match = message.to_s.match(/retry after ([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8} UTC)/i)
          return nil unless match

          Time.parse(match[1])
        rescue ArgumentError
          nil
        end

        def error_code_for(message)
          rate_limited_error?(message) ? "rate_limited" : "server_error"
        end

        def error_status_for(message)
          rate_limited_error?(message) ? :too_many_requests : :service_unavailable
        end

        def rate_limited_error?(message)
          value = message.to_s.downcase
          value.include?("retry after") || value.include?("too many failed authorizations")
        end
      end
    end
  end
end
