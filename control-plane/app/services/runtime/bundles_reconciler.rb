# frozen_string_literal: true

module Runtime
  class BundlesReconciler
    def initialize(
      runtime: Devopsellence::RuntimeConfig.current,
      broker: nil,
      organization_provisioner_class: OrganizationBundles::Provisioner,
      environment_provisioner_class: EnvironmentBundles::Provisioner,
      node_provisioner_class: NodeBundles::Provisioner,
      warm_server_reconciler_class: WarmServers::PoolReconciler
    )
      @runtime = runtime
      @broker = broker || Runtime::Broker.current
      @organization_provisioner_class = organization_provisioner_class
      @environment_provisioner_class = environment_provisioner_class
      @node_provisioner_class = node_provisioner_class
      @warm_server_reconciler_class = warm_server_reconciler_class
    end

    def call
      RuntimeProject.default!

      with_reconciler_lock do
        ensure_warm_servers!

        RuntimeProject.find_each do |runtime_project|
          Rails.logger.info("[bundles_reconciler] reconciling bundles runtime_project=#{runtime_project_identifier(runtime_project)} org_bundle_target=#{runtime.organization_bundle_target} env_bundle_target=#{runtime.environment_bundle_target} node_bundle_target=#{runtime.node_bundle_target}")
          ensure_organization_bundles!(runtime_project:)
        end
      end
    end

    private

    attr_reader :runtime, :broker, :organization_provisioner_class, :environment_provisioner_class,
      :node_provisioner_class, :warm_server_reconciler_class

    def ensure_organization_bundles!(runtime_project:)
      OrganizationBundle.where(runtime_project: runtime_project, status: ready_descendant_statuses).find_each do |bundle|
        ensure_environment_bundles!(organization_bundle: bundle)
      end

      ensure_target!(
        OrganizationBundle.where(runtime_project: runtime_project),
        runtime.organization_bundle_target.to_i
      ) do
        organization_bundle = organization_provisioner_class.new(runtime_project:, broker:).call
        ensure_environment_bundles!(organization_bundle:)
        organization_bundle
      end
    end

    def ensure_environment_bundles!(organization_bundle:)
      organization_bundle.environment_bundles.where(status: ready_descendant_statuses).find_each do |bundle|
        ensure_node_bundles!(environment_bundle: bundle)
      end

      ensure_target!(
        organization_bundle.environment_bundles,
        runtime.environment_bundle_target.to_i
      ) do
        environment_bundle = environment_provisioner_class.new(organization_bundle:, broker:).call
        ensure_node_bundles!(environment_bundle:)
        environment_bundle
      end
    end

    def ensure_node_bundles!(environment_bundle:)
      ensure_target!(
        environment_bundle.node_bundles,
        runtime.node_bundle_target.to_i
      ) do
        node_provisioner_class.new(environment_bundle:, broker:).call
      end
    end

    def ensure_warm_servers!
      warm_server_reconciler_class.new(runtime:).call
    rescue StandardError => error
      Rails.logger.warn("[bundles_reconciler] warm server pool reconciliation failed: #{error.message}")
    end

    def ensure_target!(scope, target)
      return if target <= 0

      fail_stale_provisioning!(scope)

      missing = target - active_target_scope(scope).count
      missing.times { yield } if missing.positive?
    end

    def active_target_scope(scope)
      scope.where(status: warm_status).or(
        scope.where(status: provisioning_status, updated_at: provisioning_cutoff..)
      )
    end

    def fail_stale_provisioning!(scope)
      stale_scope = scope.where(status: provisioning_status).where(updated_at: ...provisioning_cutoff)
      return if stale_scope.empty?

      Rails.logger.warn("[bundles_reconciler] failing stale provisioning bundles model=#{scope.model.name} count=#{stale_scope.count}")
      stale_scope.update_all(status: failed_status, provisioning_error: "provisioning timed out", updated_at: Time.current)
    end

    def ready_descendant_statuses
      [ warm_status, OrganizationBundle::STATUS_CLAIMED ]
    end

    def with_reconciler_lock
      Runtime::AdvisoryLock.with_lock("runtime/bundles_reconciler") { yield }
    end

    def runtime_project_identifier(runtime_project)
      runtime_project.try(:slug).presence || runtime_project.try(:id).presence || runtime_project.object_id
    end

    def provisioning_cutoff
      runtime.bundle_provisioning_timeout_seconds.to_i.seconds.ago
    end

    def provisioning_status
      OrganizationBundle::STATUS_PROVISIONING
    end

    def warm_status
      OrganizationBundle::STATUS_WARM
    end

    def failed_status
      OrganizationBundle::STATUS_FAILED
    end
  end
end
