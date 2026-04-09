# frozen_string_literal: true

require "json"

module ManagedNodes
  module Providers
    class Hetzner
      API_BASE_URL = "https://api.hetzner.cloud/v1"
      Server = Struct.new(:id, :name, :status, :public_ip, :raw, keyword_init: true)

      def initialize(client: nil, token: Devopsellence::RuntimeConfig.current.hetzner_api_token, image: nil, ssh_key_name: nil, ssh_public_key: nil)
        @token = token.to_s.strip
        @image = image.to_s.strip.presence || Devopsellence::RuntimeConfig.current.hetzner_default_image
        @ssh_key_name = ssh_key_name.to_s.strip.presence || default_ssh_key_name
        @ssh_public_key = ssh_public_key.to_s.strip.presence || default_ssh_public_key
        @client = client || RestClient.new(base_url: API_BASE_URL, token: @token)
      end

      def create_server(name:, region:, size_slug:, user_data:)
        ssh_key_name = ensure_ssh_key_name
        ensure_configured!

        payload = {
          name: name,
          server_type: size_slug,
          location: region,
          image: image,
          public_net: { ipv4_enabled: true, ipv6_enabled: true },
          user_data: user_data
        }
        payload[:ssh_keys] = [ ssh_key_name ] if ssh_key_name.present?

        response = client.post("/servers", payload: payload)

        raise "hetzner server create failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

        parse_server(JSON.parse(response.body).fetch("server"))
      end

      def delete_server(provider_server_id:)
        ensure_configured!

        response = client.delete("/servers/#{provider_server_id}")
        return if response.code.to_i == 404
        return if response.code.to_i.between?(200, 299)

        raise "hetzner server delete failed (#{response.code}): #{utf8(response.body)}"
      end

      def list_servers
        ensure_configured!

        page = 1
        servers = []

        loop do
          response = client.get("/servers?page=#{page}")
          raise "hetzner server list failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

          payload = JSON.parse(response.body)
          servers.concat(Array(payload.fetch("servers", [])).map { |entry| parse_server(entry) })

          next_page = payload.dig("meta", "pagination", "next_page")
          break unless next_page

          page = next_page.to_i
        end

        servers
      end

      def server(provider_server_id:)
        ensure_configured!

        response = client.get("/servers/#{provider_server_id}")
        raise "hetzner server lookup failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

        parse_server(JSON.parse(response.body).fetch("server"))
      end

      def ready?(server)
        server.status == "running"
      end

      def public_ip(server)
        server.public_ip
      end

      private

      attr_reader :client, :token, :image, :ssh_key_name, :ssh_public_key

      def default_ssh_key_name
        return unless managed_ssh_key_defaults_enabled?

        Devopsellence::RuntimeConfig.current.hetzner_ssh_key_name
      end

      def default_ssh_public_key
        return unless managed_ssh_key_defaults_enabled?

        Devopsellence::RuntimeConfig.current.hetzner_ssh_public_key
      end

      def managed_ssh_key_defaults_enabled?
        Rails.env.development?
      end

      def ensure_configured!
        raise "configure DEVOPSELLENCE_HETZNER_API_TOKEN for managed Hetzner nodes" if token.blank?
      end

      def ensure_ssh_key_name
        return nil if ssh_key_name.blank? || ssh_public_key.blank?

        existing = find_ssh_key_by_name(ssh_key_name)
        return existing.fetch("name") if existing

        response = client.post("/ssh_keys", payload: {
          name: ssh_key_name,
          public_key: ssh_public_key
        })
        raise "hetzner ssh key create failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

        JSON.parse(response.body).fetch("ssh_key").fetch("name")
      end

      def find_ssh_key_by_name(name)
        response = client.get("/ssh_keys")
        raise "hetzner ssh key list failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

        Array(JSON.parse(response.body).fetch("ssh_keys", [])).find { |entry| entry["name"].to_s == name.to_s }
      end

      def parse_server(payload)
        Server.new(
          id: payload.fetch("id").to_s,
          name: payload["name"].to_s,
          status: payload["status"].to_s,
          public_ip: payload.dig("public_net", "ipv4", "ip"),
          raw: payload
        )
      end

      def utf8(value)
        value.to_s.encode("UTF-8", invalid: :replace, undef: :replace, replace: "?")
      end
    end
  end
end
