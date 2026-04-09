# frozen_string_literal: true

module Environments
  class Delete
    Result = Struct.new(
      :environment_id,
      :environment_name,
      :project_id,
      :project_name,
      :customer_node_ids,
      :managed_node_ids,
      keyword_init: true
    )

    def initialize(environment:)
      @environment = environment
    end

    def call
      customer_node_ids = []
      managed_node_ids = []

      environment.nodes.order(:id).find_each do |node|
        if node.managed?
          Nodes::Cleanup.new(node:).call
          managed_node_ids << node.id
        else
          Nodes::Unassign.new(node:).call
          customer_node_ids << node.id
        end
      end

      environment_bundle = environment.environment_bundle
      environment.transaction do
        environment.update!(environment_bundle: nil, current_release: nil)
        environment_bundle&.destroy!
        environment.destroy!
      end

      Runtime::EnsureBundles.enqueue if environment_bundle

      Result.new(
        environment_id: environment.id,
        environment_name: environment.name,
        project_id: environment.project_id,
        project_name: environment.project.name,
        customer_node_ids:,
        managed_node_ids:
      )
    end

    private

    attr_reader :environment
  end
end
