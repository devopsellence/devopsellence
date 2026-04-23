# frozen_string_literal: true

module NodeDesiredState
  class IngressPayload
    def self.build(node:, environment:, release:)
      ingress_config = release.ingress_config
      ingress_service_name = release.ingress_service_name
      return nil if ingress_service_name.blank?
      return nil unless release.service_scheduled_on?(ingress_service_name, node)
      return nil unless Devopsellence::IngressConfig.managed?

      ingress = environment.environment_ingress
      hosts = ingress&.hosts || []
      return nil if hosts.empty?

      payload = {
        hosts: hosts,
        tls: normalize_tls(ingress_config&.dig("tls")),
        redirectHttp: ingress_config&.key?("redirect_http") ? ingress_config["redirect_http"] : true,
        routes: routes_for(environment:, hosts:, release:)
      }.compact

      if environment.tunnel_ingress?
        return nil unless ingress.status == EnvironmentIngress::STATUS_READY

        payload.merge(
          mode: Environment::INGRESS_STRATEGY_TUNNEL,
          tunnelTokenSecretRef: ingress.tunnel_token_secret_ref
        )
      else
        return nil unless node.supports_capability?(Node::CAPABILITY_DIRECT_DNS_INGRESS)

        payload.merge(
          mode: "public",
        )
      end
    end

    def self.routes_for(environment:, hosts:, release:)
      hosts.map do |host|
        {
          match: {
            hostname: host
          },
          target: {
            environment: environment.name,
            service: release.ingress_service_name,
            port: "http"
          }
        }
      end
    end

    def self.normalize_tls(tls)
      return { mode: "auto" } unless tls.is_a?(Hash)

      {
        mode: tls["mode"].presence || "auto",
        email: tls["email"],
        caDirectoryUrl: tls["ca_directory_url"]
      }.compact
    end
  end
end
