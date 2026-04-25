# frozen_string_literal: true

module NodeDesiredState
  class IngressPayload
    def self.build(node:, environment:, release:)
      target_services = release.ingress_target_service_names
      return nil if target_services.blank?
      return nil unless release.ingress_scheduled_on?(node)
      return nil unless Devopsellence::IngressConfig.managed?

      ingress = environment.environment_ingress
      return nil unless ingress&.hostname.present?

      hosts = configured_hosts(release)
      hosts = [ingress.hostname] if hosts.empty?

      tls = configured_tls(release)
      redirect_http = configured_redirect_http(release)

      if environment.tunnel_ingress?
        return nil unless ingress.status == EnvironmentIngress::STATUS_READY

        {
          hosts: hosts,
          mode: Environment::INGRESS_STRATEGY_TUNNEL,
          tls: tls,
          redirectHttp: redirect_http,
          tunnelTokenSecretRef: ingress.tunnel_token_secret_ref,
          routes: routes_for(environment:, ingress:, release:)
        }
      else
        return nil unless node.supports_capability?(Node::CAPABILITY_DIRECT_DNS_INGRESS)

        {
          hosts: hosts,
          mode: "public",
          tls: tls,
          redirectHttp: redirect_http,
          routes: routes_for(environment:, ingress:, release:)
        }
      end
    end

    def self.routes_for(environment:, ingress:, release:)
      Array(release.ingress_config["rules"]).map do |raw_rule|
        rule = raw_rule.is_a?(Hash) ? raw_rule : {}
        match = rule["match"].is_a?(Hash) ? rule["match"] : {}
        target = rule["target"].is_a?(Hash) ? rule["target"] : {}
        {
          match: {
            hostname: IngressHostnames.normalize(match["host"].to_s.strip.presence || ingress.hostname),
            pathPrefix: match["path_prefix"].to_s.strip.presence || "/"
          },
          target: {
            environment: environment.name,
            service: target["service"],
            port: target["port"]
          }
        }
      end
    end

    def self.configured_hosts(release)
      hosts = Array(release.ingress_config["hosts"]).filter_map do |host|
        value = host.to_s.strip
        value.presence
      end

      IngressHostnames.normalize_all(hosts)
    end

    def self.configured_tls(release)
      tls = release.ingress_config["tls"]
      tls = tls.is_a?(Hash) ? tls : {}
      {
        mode: tls["mode"].to_s.strip.presence || "auto",
        email: tls["email"].to_s.strip.presence,
        caDirectoryUrl: tls["ca_directory_url"].to_s.strip.presence
      }.compact
    end

    def self.configured_redirect_http(release)
      return release.ingress_config["redirect_http"] if release.ingress_config.key?("redirect_http")

      true
    end
  end
end
