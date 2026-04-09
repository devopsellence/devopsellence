# frozen_string_literal: true

module ManagedNodes
  class DeleteJob < ApplicationJob
    queue_as :default

    def perform(node_id:)
      node = Node.find_by(id: node_id)
      return unless node&.managed?

      Rails.logger.info("[managed_nodes/delete_job] deleting managed node node_id=#{node.id} name=#{node.name} provider_server_id=#{node.provider_server_id}")
      node_bundle = node.node_bundle
      ManagedNodes::DeleteServer.new(node: node).call
      node_bundle&.destroy!
      node.destroy!
      Rails.logger.info("[managed_nodes/delete_job] node deleted node_id=#{node_id}")
      Runtime::EnsureBundles.enqueue if node_bundle
    end
  end
end
