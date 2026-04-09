# frozen_string_literal: true

require "ipaddr"

module Devopsellence
  module TrustedProxyCidrs
    class << self
      def load(path:)
        return [] unless path.exist?

        path.readlines.filter_map do |line|
          cidr = line.to_s.strip
          next if cidr.blank?

          IPAddr.new(cidr)
        end
      end
    end
  end
end
