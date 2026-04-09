# frozen_string_literal: true

module ManagedNodes
  class ExpireLeasesJob < ApplicationJob
    queue_as :default

    def perform(now: Time.current)
      expired = Node.where(managed: true)
        .where.not(environment_id: nil)
        .where.not(lease_expires_at: nil)
        .where("lease_expires_at <= ?", now)

      count = expired.count
      Rails.logger.info("[expire_leases_job] expiring #{count} managed node lease(s)") if count > 0

      expired.find_each do |node|
        ManagedNodes::RetireNode.new(node:, revoked_at: now).call
      end

      # Return nodes that were reserved during warm server claim but never fully assigned.
      # This can happen if the assignment process fails after WarmServers::Claim sets lease_expires_at
      # but before NodeBundles::Claim#associate_node! finishes. In that failure window, a node bundle
      # may already be marked claimed, so release both the lease and any partially claimed bundle.
      stuck = Node.where(managed: true, environment_id: nil, organization_id: nil)
        .where.not(lease_expires_at: nil)
        .where("lease_expires_at <= ?", now)

      stuck_count = stuck.count
      Rails.logger.info("[expire_leases_job] returning #{stuck_count} stuck node(s) to warm pool") if stuck_count > 0
      stuck.find_each do |node|
        release_stuck_claim!(node)
      end
    end

    private

    def release_stuck_claim!(node)
      Node.transaction do
        node.lock!
        bundle = node.node_bundle || NodeBundle.lock.find_by(node_id: node.id)

        if bundle&.status == NodeBundle::STATUS_CLAIMED
          bundle.update_columns(node_id: nil, claimed_at: nil, status: NodeBundle::STATUS_WARM, updated_at: Time.current)
        end

        attributes = {
          lease_expires_at: nil,
          updated_at: Time.current
        }
        if bundle || node.node_bundle_id.present?
          attributes[:node_bundle_id] = nil
          attributes[:desired_state_bucket] = ""
          attributes[:desired_state_object_path] = ""
        end

        node.update_columns(attributes)
      end
    end
  end
end
