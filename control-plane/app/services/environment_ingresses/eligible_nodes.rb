# frozen_string_literal: true

module EnvironmentIngresses
  class EligibleNodes
    def initialize(environment:, stale_node_after: ManagedNodes::EnsureCapacity::STALE_NODE_AFTER, clock: nil)
      @environment = environment
      @stale_node_after = stale_node_after
      @clock = clock || -> { Time.current }
    end

    def call
      release = environment.current_release
      return [] unless release

      candidates = environment.nodes.order(:created_at).select do |node|
        candidate_node?(node, release:)
      end
      latest_statuses = latest_deployment_node_statuses_by_node_id(candidates.map(&:id), release:)

      candidates.select do |node|
        latest_statuses[node.id]&.phase == DeploymentNodeStatus::PHASE_SETTLED
      end
    end

    private

    attr_reader :environment, :stale_node_after, :clock

    def candidate_node?(node, release:)
      return false unless release.ingress_scheduled_on?(node)
      return false if node.public_ip.to_s.strip.blank?
      return false if node.provisioning_status != Node::PROVISIONING_READY
      return false unless node.supports_capability?(Node::CAPABILITY_DIRECT_DNS_INGRESS)
      return false unless fresh?(node)

      true
    end

    def fresh?(node)
      return true if node.last_seen_at.blank?

      node.last_seen_at >= clock.call - stale_node_after
    end

    def latest_deployment_node_statuses_by_node_id(node_ids, release:)
      return {} if node_ids.empty?

      DeploymentNodeStatus
        .joins(:deployment)
        .where(node_id: node_ids, deployments: { environment_id: environment.id, release_id: release.id })
        .order("deployment_node_statuses.node_id ASC, deployments.sequence DESC, deployment_node_statuses.reported_at DESC, deployment_node_statuses.id DESC")
        .each_with_object({}) do |status, statuses_by_node_id|
          statuses_by_node_id[status.node_id] ||= status
        end
    end
  end
end
