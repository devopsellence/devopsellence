# frozen_string_literal: true

module Deployments
  class ProgressRecorder
    ERROR_GRACE_PERIOD = 5.seconds

    Result = Struct.new(:tracked, :deployment_node_status, keyword_init: true)

    def initialize(node:, status:)
      @node = node
      @status = status
    end

    def call
      ingress_changed = update_ingress_status!
      return Result.new(tracked: false) unless node.environment_id

      deployment_node_status = find_deployment_node_status(node.environment_id)
      if deployment_node_status.blank?
        enqueue_ingress_reconcile! if ingress_changed
        return Result.new(tracked: false)
      end

      phase = normalize_phase(status[:phase])
      attributes = normalized_attributes_for(deployment_node_status, phase)

      if status_changed?(deployment_node_status, attributes)
        deployment_node_status.update!(attributes)
      end

      handle_release_command_status!(deployment_node_status)
      refresh_rollout!(deployment_node_status.deployment) if rollout_refresh_needed?(deployment_node_status)
      enqueue_ingress_reconcile! if ingress_changed || phase == DeploymentNodeStatus::PHASE_SETTLED || phase == DeploymentNodeStatus::PHASE_ERROR

      Result.new(tracked: true, deployment_node_status: deployment_node_status)
    end

    private

    attr_reader :node, :status

    def update_ingress_status!
      ingress = status[:ingress]
      return false unless ingress.is_a?(Hash)

      tls_status = ingress[:tls_status].to_s.strip
      tls_not_after = parse_time(ingress[:tls_not_after])
      tls_error = ingress[:tls_error].to_s.presence
      changed =
        node.ingress_tls_status != tls_status ||
        node.ingress_tls_not_after != tls_not_after ||
        node.ingress_tls_last_error != tls_error
      return false unless changed

      node.update!(
        ingress_tls_status: tls_status,
        ingress_tls_not_after: tls_not_after,
        ingress_tls_last_error: tls_error
      )
      true
    end

    def find_deployment_node_status(environment_id)
      scope = DeploymentNodeStatus
        .joins(deployment: :release)
        .where(node_id: node.id, deployments: { environment_id: environment_id })
        .order("deployments.sequence DESC")

      if status[:revision].present?
        scope = scope.where(releases: { revision: status[:revision] })
      end

      scope.first
    end

    def normalize_phase(phase)
      case phase.to_s
      when DeploymentNodeStatus::PHASE_RECONCILING, DeploymentNodeStatus::PHASE_SETTLED, DeploymentNodeStatus::PHASE_ERROR
        phase.to_s
      else
        DeploymentNodeStatus::PHASE_RECONCILING
      end
    end

    def normalized_attributes_for(deployment_node_status, phase)
      {
        phase: phase,
        message: status[:message].presence,
        error_message: status[:error].presence,
        environments: normalized_environments,
        reported_at: reported_at_for(deployment_node_status, phase)
      }
    end

    def status_changed?(deployment_node_status, attributes)
      deployment_node_status.phase != attributes[:phase] ||
        deployment_node_status.message != attributes[:message] ||
        deployment_node_status.error_message != attributes[:error_message] ||
        deployment_node_status.environments != attributes[:environments]
    end

    def reported_at_for(deployment_node_status, phase)
      return deployment_node_status.reported_at if phase == DeploymentNodeStatus::PHASE_ERROR && deployment_node_status.phase == DeploymentNodeStatus::PHASE_ERROR && deployment_node_status.reported_at.present?

      status[:time].presence || Time.current
    end

    def rollout_refresh_needed?(deployment_node_status)
      deployment = deployment_node_status.deployment
      deployment.status == Deployment::STATUS_ROLLING_OUT || deployment.finished_at.blank?
    end

    def refresh_rollout!(deployment)
      if deployment.release_command_active?
        deployment.update!(
          finished_at: nil,
          status: Deployment::STATUS_ROLLING_OUT,
          status_message: release_command_status_message(deployment)
        )
        return
      end

      counts = deployment.deployment_node_statuses.group(:phase).count
      pending = counts.fetch(DeploymentNodeStatus::PHASE_PENDING, 0)
      reconciling = counts.fetch(DeploymentNodeStatus::PHASE_RECONCILING, 0)
      error_count = counts.fetch(DeploymentNodeStatus::PHASE_ERROR, 0)
      active = pending + reconciling
      latest_error_reported_at = nil
      error_message = nil

      if error_count.positive?
        latest_error_reported_at, error_message = deployment.deployment_node_statuses
          .where(phase: DeploymentNodeStatus::PHASE_ERROR)
          .where.not(error_message: [ nil, "" ])
          .order(reported_at: :desc, updated_at: :desc)
          .limit(1)
          .pick(:reported_at, :error_message)
      end

      failed = active.zero? && error_count.positive? && error_grace_elapsed?(latest_error_reported_at)
      finished_at = if pending.zero? && reconciling.zero? && (error_count.zero? || failed)
        deployment.finished_at || Time.current
      end
      status_value =
        if failed
          Deployment::STATUS_FAILED
        elsif finished_at.present?
          Deployment::STATUS_PUBLISHED
        else
          Deployment::STATUS_ROLLING_OUT
        end

      deployment.update!(
        finished_at: finished_at,
        status: status_value,
        status_message: rollout_status_message(counts, finished_at: finished_at),
        error_message: error_message
      )
    end

    def rollout_status_message(counts, finished_at:)
      return "publish failed" if finished_at.present? && counts.fetch(DeploymentNodeStatus::PHASE_ERROR, 0).positive?
      return "node reported error, waiting for retry" if counts.fetch(DeploymentNodeStatus::PHASE_ERROR, 0).positive?
      return "node reconciling" if finished_at.blank? && counts.fetch(DeploymentNodeStatus::PHASE_RECONCILING, 0).positive?
      return "waiting for node reconcile" if finished_at.blank?
      return "rollout settled" if counts.fetch(DeploymentNodeStatus::PHASE_SETTLED, 0).positive?

      "waiting for node reconcile"
    end

    def handle_release_command_status!(deployment_node_status)
      task = status[:task]
      return unless task.is_a?(Hash)
      return unless task[:name].to_s == "release_command"

      deployment = deployment_node_status.deployment
      return unless deployment.release_command_node_id == node.id

      case task[:phase].to_s
      when DeploymentNodeStatus::PHASE_RECONCILING
        deployment.update!(
          status: Deployment::STATUS_ROLLING_OUT,
          status_message: "running release command",
          release_command_status: Deployment::RELEASE_COMMAND_STATUS_RUNNING,
          finished_at: nil,
          error_message: nil
        )
      when DeploymentNodeStatus::PHASE_SETTLED
        deployment.update!(
          status: Deployment::STATUS_ROLLING_OUT,
          status_message: "publishing desired state",
          release_command_status: Deployment::RELEASE_COMMAND_STATUS_SUCCEEDED,
          finished_at: nil,
          error_message: nil
        )
        Deployments::PublishJob.perform_later(deployment.id)
      when DeploymentNodeStatus::PHASE_ERROR
        deployment.update!(
          status: Deployment::STATUS_FAILED,
          status_message: "release command failed",
          release_command_status: Deployment::RELEASE_COMMAND_STATUS_FAILED,
          finished_at: Time.current,
          error_message: status[:error].presence || task[:error].presence || deployment_node_status.error_message
        )
      end
    end

    def release_command_status_message(deployment)
      case deployment.release_command_status
      when Deployment::RELEASE_COMMAND_STATUS_PENDING
        "waiting to run release command"
      when Deployment::RELEASE_COMMAND_STATUS_RUNNING
        "running release command"
      when Deployment::RELEASE_COMMAND_STATUS_SUCCEEDED
        "publishing desired state"
      else
        "running release command"
      end
    end

    def error_grace_elapsed?(reported_at)
      return true if reported_at.blank?

      reported_at <= ERROR_GRACE_PERIOD.ago
    end

    def enqueue_ingress_reconcile!
      return unless node.environment_id

      EnvironmentIngresses::ReconcileJob.perform_later(node.environment_id)
    end

    def normalized_environments
      Array(status[:environments]).map do |environment|
        environment.to_h.deep_stringify_keys
      end
    end

    def parse_time(value)
      return nil if value.blank?

      Time.zone.parse(value.to_s)
    rescue ArgumentError
      nil
    end
  end
end
