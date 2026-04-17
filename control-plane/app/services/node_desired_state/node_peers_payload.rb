# frozen_string_literal: true

module NodeDesiredState
  class NodePeersPayload
    def self.build(node:, environment:)
      environment.nodes
        .reject { |peer| peer.id == node.id }
        .map { |peer| payload_for(peer) }
        .sort_by { |peer| peer.fetch(:name).to_s }
    end

    def self.payload_for(node)
      public_address = node.public_ip.to_s.strip
      {
        name: node.name.to_s,
        labels: node.labels,
        public_address: public_address.presence
      }.compact
    end
  end
end
