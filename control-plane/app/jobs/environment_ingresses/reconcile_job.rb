# frozen_string_literal: true

module EnvironmentIngresses
  class ReconcileJob < ApplicationJob
    queue_as :default

    def perform(environment_id)
      environment = load_environment(environment_id)
      return unless environment

      desired_state_signatures_before = desired_state_ingress_signatures(environment)
      EnvironmentIngresses::Reconciler.new(environment:).call
      environment = load_environment(environment_id)
      return unless environment

      if desired_state_ingress_signatures(environment) != desired_state_signatures_before
        Environments::RepublishDesiredStateJob.perform_later(environment.id)
      end
    end

    private

      def load_environment(environment_id)
        Environment.includes(:environment_ingress, :current_release, nodes: :deployment_node_statuses).find_by(id: environment_id)
      end

      def desired_state_ingress_signatures(environment)
        release = environment.current_release
        return {} unless release

        environment.nodes.order(:id).each_with_object({}) do |node, signatures|
          signatures[node.id] = NodeDesiredState::IngressPayload.build(node:, environment:, release:)
        end
      end
  end
end
