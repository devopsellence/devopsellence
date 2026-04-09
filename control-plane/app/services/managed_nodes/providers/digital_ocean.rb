# frozen_string_literal: true

require "json"

module ManagedNodes
  module Providers
    class DigitalOcean
      API_BASE_URL = "https://api.digitalocean.com/v2"
      Server = Struct.new(:id, :status, :public_ip, :raw, keyword_init: true)

      def initialize(client: nil, token: Devopsellence::RuntimeConfig.current.digitalocean_api_token, image: nil, ssh_key_name: nil, ssh_public_key: nil)
        @token = token.to_s.strip
        @image = image.to_s.strip.presence || Devopsellence::RuntimeConfig.current.digitalocean_default_image
        @ssh_key_name = ssh_key_name.to_s.strip.presence || default_ssh_key_name
        @ssh_public_key = ssh_public_key.to_s.strip.presence || default_ssh_public_key
        @client = client || ManagedNodes::RestClient.new(base_url: API_BASE_URL, token: @token)
      end

      def create_server(name:, region:, size_slug:, user_data:)
        ssh_key_fingerprint = ensure_ssh_key_fingerprint
        ensure_configured!

        payload = {
          name: name,
          region: region,
          size: size_slug,
          image: image,
          ipv6: true,
          user_data: user_data
        }
        payload[:ssh_keys] = [ ssh_key_fingerprint ] if ssh_key_fingerprint.present?

        response = client.post("/droplets", payload: payload)
        raise "digitalocean droplet create failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

        parse_server(JSON.parse(response.body).fetch("droplet"))
      end

      def delete_server(provider_server_id:)
        ensure_configured!

        response = client.delete("/droplets/#{provider_server_id}")
        return if response.code.to_i == 404
        return if response.code.to_i.between?(200, 299)

        raise "digitalocean droplet delete failed (#{response.code}): #{utf8(response.body)}"
      end

      def server(provider_server_id:)
        ensure_configured!

        response = client.get("/droplets/#{provider_server_id}")
        raise "digitalocean droplet lookup failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

        parse_server(JSON.parse(response.body).fetch("droplet"))
      end

      def ready?(server)
        server.status == "active"
      end

      def public_ip(server)
        server.public_ip
      end

      private

      attr_reader :client, :token, :image, :ssh_key_name, :ssh_public_key

      def default_ssh_key_name
        return unless managed_ssh_key_defaults_enabled?

        Devopsellence::RuntimeConfig.current.digitalocean_ssh_key_name
      end

      def default_ssh_public_key
        return unless managed_ssh_key_defaults_enabled?

        Devopsellence::RuntimeConfig.current.digitalocean_ssh_public_key
      end

      def managed_ssh_key_defaults_enabled?
        Rails.env.development?
      end

      def ensure_configured!
        raise "configure DEVOPSELLENCE_DIGITALOCEAN_API_TOKEN for managed DigitalOcean nodes" if token.blank?
      end

      def ensure_ssh_key_fingerprint
        return nil if ssh_key_name.blank? || ssh_public_key.blank?

        existing = find_ssh_key_by_name(ssh_key_name)
        return existing.fetch("fingerprint") if existing

        response = client.post("/account/keys", payload: {
          name: ssh_key_name,
          public_key: ssh_public_key
        })
        raise "digitalocean ssh key create failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

        JSON.parse(response.body).fetch("ssh_key").fetch("fingerprint")
      end

      def find_ssh_key_by_name(name)
        response = client.get("/account/keys")
        raise "digitalocean ssh key list failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

        Array(JSON.parse(response.body).fetch("ssh_keys", [])).find { |entry| entry["name"].to_s == name.to_s }
      end

      def parse_server(payload)
        public_network = Array(payload.dig("networks", "v4")).find { |entry| entry["type"].to_s == "public" }

        Server.new(
          id: payload.fetch("id").to_s,
          status: payload["status"].to_s,
          public_ip: public_network&.fetch("ip_address", nil),
          raw: payload
        )
      end

      def utf8(value)
        value.to_s.encode("UTF-8", invalid: :replace, undef: :replace, replace: "?")
      end
    end
  end
end
