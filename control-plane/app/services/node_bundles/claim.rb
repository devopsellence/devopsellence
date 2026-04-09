# frozen_string_literal: true

module NodeBundles
  class Claim
    EXISTING_PROVISIONING_WAIT_TIMEOUT = 45.seconds
    EXISTING_PROVISIONING_POLL_INTERVAL = 1.second

    Error = Class.new(StandardError)
    Result = Struct.new(:bundle, :node, keyword_init: true)

    def initialize(environment:, node:, clock: nil,
                   lease_minutes: Devopsellence::RuntimeConfig.current.managed_lease_minutes.to_i,
                   broker: nil, provisioner_class: Provisioner, publish_assignment_state: true,
                   existing_provisioning_wait_timeout: EXISTING_PROVISIONING_WAIT_TIMEOUT,
                   existing_provisioning_poll_interval: EXISTING_PROVISIONING_POLL_INTERVAL,
                   sleeper: nil)
      @environment = environment
      @node = node
      @clock = clock || -> { Time.current }
      @lease_minutes = lease_minutes
      @broker = broker || Runtime::Broker.current
      @provisioner_class = provisioner_class
      @publish_assignment_state = publish_assignment_state
      @existing_provisioning_wait_timeout = existing_provisioning_wait_timeout
      @existing_provisioning_poll_interval = existing_provisioning_poll_interval
      @sleeper = sleeper || ->(duration) { sleep(duration) }
    end

    def call
      environment_bundle = environment.environment_bundle
      raise Error, "environment bundle is required" unless environment_bundle

      bundle = reserve_bundle(environment_bundle)
      associate_node!(bundle)
      publish_desired_state! if publish_assignment_state
      Runtime::EnsureBundles.enqueue
      Result.new(bundle:, node:)
    rescue StandardError => error
      cleanup_failed_claim!(bundle) if bundle
      raise error if error.is_a?(Error)

      raise Error, error.message
    end

    private

    attr_reader :environment, :node, :clock, :lease_minutes, :broker, :provisioner_class, :publish_assignment_state

    def reserve_bundle(environment_bundle)
      bundle = warm_bundle(environment_bundle)
      if bundle.blank?
        bundle = wait_for_provisioning_bundle(environment_bundle)
      end
      if bundle.blank?
        bundle = provisioner_class.new(environment_bundle:, broker:).call
      end
      bundle.with_lock do
        raise Error, "node bundle is no longer available" unless bundle.status == NodeBundle::STATUS_WARM

        bundle.update!(
          claimed_at: clock.call,
          status: NodeBundle::STATUS_CLAIMED
        )
      end
      bundle
    end

    def warm_bundle(environment_bundle)
      environment_bundle.node_bundles.where(status: NodeBundle::STATUS_WARM).order(:created_at).first
    end

    def wait_for_provisioning_bundle(environment_bundle)
      deadline = clock.call + existing_provisioning_wait_timeout

      loop do
        bundle = warm_bundle(environment_bundle)
        if bundle.present?
          return bundle
        end

        bundle = provisioning_bundle(environment_bundle)
        if bundle.blank?
          return nil
        end

        bundle.reload
        if bundle.status == NodeBundle::STATUS_WARM
          return bundle
        end
        if bundle.status != NodeBundle::STATUS_PROVISIONING
          return nil
        end
        if clock.call >= deadline
          return nil
        end

        sleeper.call(existing_provisioning_poll_interval)
      end
    end

    def provisioning_bundle(environment_bundle)
      environment_bundle.node_bundles.where(status: NodeBundle::STATUS_PROVISIONING).order(:created_at).first
    end

    def associate_node!(bundle)
      organization = environment.project.organization
      bundle.update!(node:)
      node.update!(
        organization: organization,
        environment: environment,
        node_bundle: bundle,
        desired_state_bucket: bundle.desired_state_bucket,
        desired_state_object_path: bundle.desired_state_object_path,
        desired_state_sequence: bundle.desired_state_sequence,
        lease_expires_at: node.managed? ? clock.call + lease_minutes.minutes : nil
      )
    end

    def publish_desired_state!
      desired_state = Nodes::DesiredStatePublisher.new(node:).call
      raise Error, "desired state publish failed" if desired_state.uri.blank?
    end

    def cleanup_failed_claim!(bundle)
      Node.transaction do
        node.reload
        bundle.reload
        node.lock!
        bundle.lock!

        return if node.environment_id == environment.id && node.node_bundle_id == bundle.id

        if bundle.status == NodeBundle::STATUS_CLAIMED && (bundle.node_id.nil? || bundle.node_id == node.id)
          bundle.update_columns(node_id: nil, claimed_at: nil, status: NodeBundle::STATUS_WARM, updated_at: Time.current)
        end

        return if node.node_bundle_id.present? && node.node_bundle_id != bundle.id

        node.update_columns(
          environment_id: nil,
          node_bundle_id: nil,
          lease_expires_at: nil,
          desired_state_bucket: "",
          desired_state_object_path: "",
          updated_at: Time.current
        )
      end
    rescue StandardError => error
      Rails.logger.warn("[node_bundles/claim] failed cleaning up partially claimed bundle=#{bundle.id} node=#{node.id} error=#{error.message}")
    end

    def existing_provisioning_wait_timeout = @existing_provisioning_wait_timeout
    def existing_provisioning_poll_interval = @existing_provisioning_poll_interval
    def sleeper = @sleeper
  end
end
