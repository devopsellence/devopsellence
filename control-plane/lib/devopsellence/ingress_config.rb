# frozen_string_literal: true

module Devopsellence
  module IngressConfig
    module_function

    def local?
      runtime.ingress_backend == "local"
    end

    def managed?
      !local?
    end

    def hostname_zone_name
      return runtime.local_ingress_hostname_suffix if local?

      runtime.cloudflare_zone_name
    end

    def public_url(hostname)
      return runtime.local_ingress_public_url.presence if local?
      return nil if hostname.blank?

      "https://#{hostname}"
    end

    def envoy_origin
      runtime.cloudflare_envoy_origin
    end

    def runtime
      Devopsellence::RuntimeConfig.current
    end

    private_class_method :runtime
  end
end
