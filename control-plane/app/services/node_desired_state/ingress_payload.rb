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
      Array(release.ingress_config["rules"]).map.with_index do |raw_rule, index|
        rule = required_hash(raw_rule, field: "ingress.rules[#{index}]")
        match = required_hash(rule["match"], field: "ingress.rules[#{index}].match")
        target = required_hash(rule["target"], field: "ingress.rules[#{index}].target")
        {
          match: {
            hostname: IngressHostnames.normalize(required_string(match["host"], field: "ingress.rules[#{index}].match.host")),
            pathPrefix: match["path_prefix"].to_s.strip.presence || "/"
          },
          target: {
            environment: environment.name,
            service: required_string(target["service"], field: "ingress.rules[#{index}].target.service"),
            port: required_string(target["port"], field: "ingress.rules[#{index}].target.port")
          }
        }
      end
    end

    def self.required_hash(value, field:)
      raise Release::InvalidRuntimeConfig, "#{field} must decode to an object" unless value.is_a?(Hash)

      value
    end

    def self.required_string(value, field:)
      value = value.to_s.strip
      raise Release::InvalidRuntimeConfig, "#{field} is required" if value.blank?

      value
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
      if release.ingress_config.key?("redirect_http")
        value = release.ingress_config["redirect_http"]
        raise Release::InvalidRuntimeConfig, "ingress.redirect_http must be a boolean" unless value == true || value == false

        return value
      end

      true
    end
  end
end
