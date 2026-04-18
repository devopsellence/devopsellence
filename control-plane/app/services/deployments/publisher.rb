# frozen_string_literal: true

module Deployments
  class Publisher
    LEASE_REFRESH_WINDOW = 10.minutes
    SchedulingError = Class.new(StandardError)
    Result = Struct.new(:deployment, :assigned_nodes, keyword_init: true)

    def initialize(environment:, release:, store: Storage::ObjectStore.build, deployment: nil)
      @environment = environment
      @release = release
      @store = store
      @deployment = deployment
    end

    def call
      deployment = @deployment
      assigned_nodes = []

      update_progress!("waiting for managed capacity") if environment.managed_runtime?
      ensure_managed_capacity!(deployment:) if environment.managed_runtime?
      if release.ingress_service_name.present? && !ingress_ready?
        update_progress!("provisioning ingress")
        provision_ingress!
      end

      Environment.transaction do
        environment.lock!
        release.lock!
        assigned_nodes = environment.nodes.order(:created_at).to_a
        validate_assignments!(assigned_nodes)
        extend_trial_leases!(assigned_nodes)

        sequence = deployment&.sequence || environment.deployments.maximum(:sequence).to_i + 1

        now = Time.current

        deployment =
          if deployment
            deployment.tap do |existing|
              existing.update!(
                release: release,
                sequence: sequence,
                published_at: existing.published_at || now,
                finished_at: nil,
                error_message: nil
              )
            end
          else
            environment.deployments.create!(
              release: release,
              sequence: sequence,
              request_token: generated_request_token,
              status: Deployment::STATUS_SCHEDULING,
              status_message: "waiting to publish desired state",
              published_at: Time.current
            )
          end
      end

      if release_task_stage?(deployment)
        publish_release_task!(deployment:, assigned_nodes:)
      else
        publish_runtime_rollout!(deployment:, assigned_nodes:)
      end

      Result.new(deployment: deployment, assigned_nodes: assigned_nodes.size)
    rescue StandardError => error
      mark_failed!(deployment, error)
      raise
    end

    private

    attr_reader :environment, :release, :store

    def ensure_managed_capacity!(deployment:)
      ManagedNodes::EnsureCapacity.new(
        environment: environment,
        release: release,
        issuer: Devopsellence::RuntimeConfig.current.public_base_url,
        progress: ->(message) { update_progress!(message, deployment:) },
        publish_assignment_state: false
      ).call
    end

    def validate_assignments!(nodes)
      if release.stateful? && nodes.size > 1
        raise SchedulingError, "stateful releases can only be published to a single assigned node"
      end

      release.service_names.each do |service_name|
        next if nodes.any? { |node| release.service_scheduled_on?(service_name, node) }

        label = release.service_label_for(service_name)
        raise SchedulingError, "at least one assigned node must match label #{label.inspect} for service #{service_name}"
      end

      if environment.direct_dns_ingress? && release.ingress_service_name.present?
        incompatible_nodes = nodes.select do |node|
          release.service_scheduled_on?(release.ingress_service_name, node) &&
            !node.supports_capability?(Node::CAPABILITY_DIRECT_DNS_INGRESS)
        end
        if incompatible_nodes.any?
          names = incompatible_nodes.map(&:name).sort.join(", ")
          raise SchedulingError, "assigned ingress nodes do not support direct_dns ingress: #{names}"
        end
      end

      return unless release.has_release_task?

      capable_release_nodes = release_task_executor_candidates(nodes)
      if capable_release_nodes.empty?
        raise SchedulingError, "assigned nodes for #{release.release_task_service_name} do not support release_task"
      end
    end

    def ingress_ready?
      environment.environment_ingress&.status == EnvironmentIngress::STATUS_READY
    end

    def provision_ingress!
      EnvironmentIngresses::Reconciler.new(environment: environment).call
      environment.association(:environment_ingress).reset
    end

    def extend_trial_leases!(nodes)
      return unless environment.project.organization.trial?

      lease_expires_at = Time.current + Devopsellence::RuntimeConfig.current.managed_lease_minutes.to_i.minutes
      nodes.each do |node|
        next if node.lease_expires_at.present? && node.lease_expires_at > lease_expires_at - LEASE_REFRESH_WINDOW

        node.update!(lease_expires_at: lease_expires_at)
      end
    end

    def sync_deployment_node_statuses!(deployment:, assigned_nodes:)
      existing = deployment.deployment_node_statuses.index_by(&:node_id)
      assigned_nodes.each do |node|
        record = existing.delete(node.id)
        attributes = {
          phase: DeploymentNodeStatus::PHASE_PENDING,
          message: "waiting for node to reconcile",
          error_message: nil,
          reported_at: nil
        }
        if record
          record.update!(attributes)
        else
          deployment.deployment_node_statuses.create!(attributes.merge(node: node))
        end
      end
      existing.each_value(&:destroy!)
    end

    def sync_release_task_status!(deployment:, node:)
      existing = deployment.deployment_node_statuses.index_by(&:node_id)
      record = existing.delete(node.id)
      attributes = {
        phase: DeploymentNodeStatus::PHASE_PENDING,
        message: "waiting to run release task",
        error_message: nil,
        reported_at: nil
      }
      if record
        record.update!(attributes)
      else
        deployment.deployment_node_statuses.create!(attributes.merge(node: node))
      end
      existing.each_value(&:destroy!)
    end

    def release_task_stage?(deployment)
      release.has_release_task? && deployment.release_task_status != Deployment::RELEASE_TASK_STATUS_SUCCEEDED
    end

    def publish_release_task!(deployment:, assigned_nodes:)
      executor = deployment.release_task_node || release_task_executor_candidates(assigned_nodes).first
      raise SchedulingError, "assigned nodes do not support release_task" unless executor

      deployment.update!(
        status: Deployment::STATUS_ROLLING_OUT,
        status_message: "running release task",
        release_task_status: Deployment::RELEASE_TASK_STATUS_PENDING,
        release_task_node: executor,
        finished_at: nil,
        error_message: nil
      )

      payload = ->(sequence:) do
        NodeDesiredState::ReleaseTaskBuilder.new(
          node: executor,
          environment: environment,
          release: release,
          sequence: sequence
        ).call.merge(
          assigned: true,
          desired_state_bucket: executor.desired_state_bucket,
          desired_state_object_path: executor.desired_state_object_path
        )
      end

      Nodes::DesiredStatePublisher.new(node: executor, payload: payload, store: store).call
      sync_release_task_status!(deployment:, node: executor)
    end

    def publish_runtime_rollout!(deployment:, assigned_nodes:)
      release.update!(
        desired_state_uri: nil,
        desired_state_sha256: nil,
        status: Release::STATUS_PUBLISHED,
        published_at: Time.current
      )
      environment.update!(current_release: release)
      deployment.update!(
        status: Deployment::STATUS_ROLLING_OUT,
        status_message: "publishing desired state",
        release_task_status: nil,
        finished_at: nil,
        error_message: nil
      )

      assigned_nodes.each do |node|
        Nodes::DesiredStatePublisher.new(node: node, release: release, store: store).call
      end

      sync_deployment_node_statuses!(deployment:, assigned_nodes:)
      EnvironmentIngresses::ReconcileJob.perform_later(environment.id) if release.ingress_service_name.present?
      update_progress!("waiting for node reconcile", deployment:)
    end

    def release_task_executor_candidates(nodes)
      service_name = release.release_task_service_name
      return [] if service_name.blank?

      nodes.select do |node|
        release.service_scheduled_on?(service_name, node) && node.supports_capability?(Node::CAPABILITY_RELEASE_TASK)
      end
    end

    def mark_failed!(deployment, error)
      return unless deployment&.persisted?

      deployment.update!(
        status: Deployment::STATUS_FAILED,
        status_message: "publish failed",
        finished_at: Time.current,
        error_message: error.message
      )
    rescue StandardError
      nil
    end

    def update_progress!(message, deployment: @deployment)
      return unless deployment&.persisted?

      deployment.update!(status_message: message)
    end

    def generated_request_token
      SecureRandom.hex(16)
    end
  end
end
