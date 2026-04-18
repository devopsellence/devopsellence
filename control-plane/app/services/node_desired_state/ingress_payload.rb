# frozen_string_literal: true

module NodeDesiredState
  class IngressPayload
    def self.build(node:, environment:, release:)
      ingress_service_name = release.ingress_service_name
      return nil if ingress_service_name.blank?
      return nil unless release.service_scheduled_on?(ingress_service_name, node)
      return nil unless Devopsellence::IngressConfig.managed?

      ingress = environment.environment_ingress
      return nil unless ingress&.hostname.present?

      if environment.tunnel_ingress?
        return nil unless ingress.status == EnvironmentIngress::STATUS_READY

        {
          hosts: [ ingress.hostname ],
          mode: Environment::INGRESS_STRATEGY_TUNNEL,
          tunnelTokenSecretRef: ingress.tunnel_token_secret_ref,
          routes: routes_for(environment:, ingress:, release:)
        }
      else
        return nil unless node.supports_capability?(Node::CAPABILITY_DIRECT_DNS_INGRESS)

        {
          hosts: [ ingress.hostname ],
          mode: "public",
          tls: {
            mode: "auto"
          },
          redirectHttp: true,
          routes: routes_for(environment:, ingress:, release:)
        }
      end
    end

    def self.routes_for(environment:, ingress:, release:)
      [
        {
          match: {
            hostname: ingress.hostname
          },
          target: {
            environment: environment.name,
            service: release.ingress_service_name,
            port: "http"
          }
        }
      ]
    end
  end
end
