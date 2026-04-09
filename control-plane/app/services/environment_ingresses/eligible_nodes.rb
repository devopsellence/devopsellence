# frozen_string_literal: true

module EnvironmentIngresses
  class EligibleNodes
    def initialize(environment:, stale_node_after: ManagedNodes::EnsureCapacity::STALE_NODE_AFTER, clock: nil)
      @environment = environment
      @stale_node_after = stale_node_after
      @clock = clock || -> { Time.current }
    end

    def call
      return [] unless environment.current_release

      environment.nodes.order(:created_at).select do |node|
        eligible_node?(node)
      end
    end

    private

    attr_reader :environment, :stale_node_after, :clock

    def eligible_node?(node)
      return false unless node.labeled?(Node::LABEL_WEB)
      return false if node.public_ip.to_s.strip.blank?
      return false if node.provisioning_status != Node::PROVISIONING_READY
      return false unless node.supports_capability?(Node::CAPABILITY_DIRECT_DNS_INGRESS)
      return false unless node.ingress_tls_ready?
      return false unless fresh?(node)

      latest_deployment_node_status_for(node)&.phase == DeploymentNodeStatus::PHASE_SETTLED
    end

    def fresh?(node)
      return true if node.last_seen_at.blank?

      node.last_seen_at >= clock.call - stale_node_after
    end

    def latest_deployment_node_status_for(node)
      @latest_deployment_node_statuses ||= {}
      return @latest_deployment_node_statuses[node.id] if @latest_deployment_node_statuses.key?(node.id)

      status = DeploymentNodeStatus
        .joins(:deployment)
        .where(node_id: node.id, deployments: { environment_id: environment.id, release_id: environment.current_release_id })
        .order("deployments.sequence DESC, deployment_node_statuses.reported_at DESC, deployment_node_statuses.id DESC")
        .first
      @latest_deployment_node_statuses[node.id] = status
    end
  end
end
