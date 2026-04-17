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
          hostname: ingress.hostname,
          mode: Environment::INGRESS_STRATEGY_TUNNEL,
          public_url: ingress.public_url,
          tunnel_token_secret_ref: ingress.tunnel_token_secret_ref
        }
      else
        return nil unless node.supports_capability?(Node::CAPABILITY_DIRECT_DNS_INGRESS)

        {
          hosts: [ ingress.hostname ],
          hostname: ingress.hostname,
          mode: "public",
          public_url: ingress.public_url,
          tls: {
            mode: "auto"
          },
          redirect_http: true,
          http01_peers: http01_peers_for(node:, environment:)
        }
      end
    end

    def self.http01_peers_for(node:, environment:)
      environment.nodes
        .select { |peer| public_web_peer?(peer) && peer.id != node.id }
        .filter_map { |peer| peer.public_ip.to_s.strip.presence }
        .uniq
        .sort
    end

    def self.public_web_peer?(node)
      node.labeled?(Node::LABEL_WEB) &&
        node.supports_capability?(Node::CAPABILITY_DIRECT_DNS_INGRESS)
    end
  end
end
