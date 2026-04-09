# frozen_string_literal: true

module Nodes
  class BootstrapAssignmentJob < ApplicationJob
    queue_as :default

    retry_on Nodes::AssignmentManager::Error, wait: 5.seconds, attempts: 12

    def perform(node_id:, environment_id:, issuer:)
      node = Node.find_by(id: node_id)
      environment = Environment.find_by(id: environment_id)
      return if node.blank? || environment.blank?
      return if node.revoked_at.present?
      return if node.environment_id.present? && node.environment_id != environment.id
      return if node.environment_id == environment.id && node.assignment_ready?
      return unless environment.project&.organization_id.present?
      return unless node.organization_id.nil? || node.organization_id == environment.project.organization_id

      Nodes::AssignmentManager.new(node:, environment:, issuer:).call
    end
  end
end
