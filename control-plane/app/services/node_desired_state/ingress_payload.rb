# frozen_string_literal: true

module NodeDesiredState
  class IngressPayload
    def self.build(node:, environment:, release:)
      return nil unless node.labeled?(Node::LABEL_WEB)
      return nil unless release.requires_label?(Node::LABEL_WEB)
      return nil unless Devopsellence::IngressConfig.managed?

      ingress = environment.environment_ingress
      return nil unless ingress&.hostname.present?

      if environment.tunnel_ingress?
        return nil unless ingress.status == EnvironmentIngress::STATUS_READY

        {
          hosts: [ ingress.hostname ],
          mode: Environment::INGRESS_STRATEGY_TUNNEL,
          tunnelTokenSecretRef: ingress.tunnel_token_secret_ref,
          routes: routes_for(environment:, ingress:)
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
          routes: routes_for(environment:, ingress:)
        }
      end
    end

    def self.routes_for(environment:, ingress:)
      [
        {
          match: {
            hostname: ingress.hostname
          },
          target: {
            environment: environment.name,
            service: "web",
            port: "http"
          }
        }
      ]
    end
  end
end
