# frozen_string_literal: true

module ManagedNodes
  module Providers
    class Resolver
      def self.resolve(slug, client: nil)
        case slug.to_s
        when "hetzner"
          Hetzner.new(client: client)
        when "digitalocean"
          DigitalOcean.new(client: client)
        else
          raise ArgumentError, "unsupported managed node provider #{slug.inspect}"
        end
      end
    end
  end
end
