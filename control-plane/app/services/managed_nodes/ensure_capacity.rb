# frozen_string_literal: true

module ManagedNodes
  class EnsureCapacity
    STALE_NODE_AFTER = 10.minutes
    Error  = Class.new(StandardError)
    Result = Struct.new(:nodes, :claimed_from_pool, :provisioned, keyword_init: true)

    def initialize(environment:, release:, issuer:,
                   retire_node_class: ManagedNodes::RetireNode,
                   stale_node_after: STALE_NODE_AFTER,
                   clock: nil,
                   lease_minutes: Devopsellence::RuntimeConfig.current.managed_lease_minutes.to_i,
                   progress: nil,
                   publish_assignment_state: true)
      @environment      = environment
      @release          = release
      @issuer           = issuer
      @retire_node_class = retire_node_class
      @stale_node_after  = stale_node_after
      @clock             = clock || -> { Time.current }
      @lease_minutes     = lease_minutes
      @progress          = progress
      @publish_assignment_state = publish_assignment_state
    end

    def call
      return Result.new(nodes: environment.nodes.to_a, claimed_from_pool: false, provisioned: false) unless environment.managed_runtime?

      update_progress("checking managed capacity")
      assigned_nodes = environment.nodes.order(:created_at).to_a
      usable_nodes = assigned_nodes.select { |node| assigned_node_usable?(node) }
      if usable_nodes.any?
        update_progress("using existing managed node")
        usable_nodes.each { |node| ensure_labels!(node) }
        return Result.new(nodes: usable_nodes, claimed_from_pool: false, provisioned: false)
      end

      assigned_nodes.each do |node|
        update_progress("retiring stale managed node")
        retire_assigned_node!(node)
      end

      update_progress("claiming warm server")
      node = WarmServers::Claim.new(on_progress: @progress).call

      update_progress("assigning node to environment")
      Nodes::AssignmentManager.new(
        node: node,
        environment: environment,
        issuer: issuer,
        on_progress: @progress,
        publish_assignment_state: publish_assignment_state
      ).call

      ensure_labels!(node.reload)
      Runtime::EnsureBundles.enqueue

      Result.new(nodes: [ node ], claimed_from_pool: true, provisioned: false)
    rescue WarmServers::Claim::Error, Nodes::AssignmentManager::Error => error
      raise Error, error.message
    end

    private

    attr_reader :environment, :release, :issuer

    def assigned_node_usable?(node)
      return false unless node.managed?
      return false if node.revoked_at.present?
      return false unless node.provisioning_status == Node::PROVISIONING_READY
      return false if node.lease_expires_at.present? && node.lease_expires_at <= clock.call
      return true if node.last_seen_at.blank?

      node.last_seen_at >= clock.call - stale_node_after
    end

    def retire_assigned_node!(node)
      retire_node(node)
    rescue StandardError
      nil
    end

    def ensure_labels!(node)
      labels = required_labels
      return if node.labels == labels

      node.labels = labels
      node.save!
    end

    def required_labels
      release.required_roles.presence || [ Node::DEFAULT_LABEL ]
    end

    def update_progress(message)
      @progress&.call(message)
    rescue StandardError
      nil
    end

    def retire_node(node)
      @retire_node_class.new(node: node, revoked_at: clock.call).call
    end

    def stale_node_after = @stale_node_after
    def clock            = @clock
    def publish_assignment_state = @publish_assignment_state
  end
end
