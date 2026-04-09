# frozen_string_literal: true

module ManagedNodes
  class DeleteServer
    Error = Class.new(StandardError)

    def initialize(node:, provider: nil)
      @node = node
      @provider = provider
    end

    def call
      return if node.provider_server_id.blank?

      node.update!(provisioning_status: Node::PROVISIONING_DELETING)
      provider.delete_server(provider_server_id: node.provider_server_id)
    rescue StandardError => error
      node.update!(provisioning_status: Node::PROVISIONING_FAILED, provisioning_error: error.message)
      raise Error, error.message
    end

    private

    attr_reader :node

    def provider
      @provider ||= Providers::Resolver.resolve(node.managed_provider)
    end
  end
end
