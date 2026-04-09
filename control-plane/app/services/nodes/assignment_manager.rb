# frozen_string_literal: true

module Nodes
  class AssignmentManager
    Error = Class.new(StandardError)
    Result = Struct.new(:node, :environment, :previous_environment, keyword_init: true)

    def initialize(node:, environment:, issuer:, broker: nil, on_progress: nil, publish_assignment_state: true)
      @node = node
      @environment = environment
      @issuer = issuer
      @broker = broker || Runtime::Broker.current
      @on_progress = on_progress
      @publish_assignment_state = publish_assignment_state
    end

    def call
      node.with_lock do
        validate_assignment_scope!

        previous_environment = node.environment
        if assignment_satisfied?
          enqueue_ingress_reconcile!(previous_environment)
          enqueue_ingress_reconcile!(environment)
          return Result.new(node:, environment:, previous_environment:)
        end

        if recoverable_partial_assignment?
          on_progress&.call("publishing desired state")
          repair_partial_assignment!
          environment.update!(runtime_kind: Environment::RUNTIME_CUSTOMER_NODES) unless node.managed?
          enqueue_ingress_reconcile!(previous_environment)
          enqueue_ingress_reconcile!(environment)
          return Result.new(node:, environment:, previous_environment:)
        end

        on_progress&.call("ensuring organization bundle")
        ensure_organization_bundle!

        on_progress&.call("ensuring environment bundle")
        ensure_environment_bundle!

        on_progress&.call("claiming node bundle")
        NodeBundles::Claim.new(
          environment: environment,
          node: node,
          broker: broker,
          publish_assignment_state: publish_assignment_state
        ).call

        environment.update!(runtime_kind: Environment::RUNTIME_CUSTOMER_NODES) unless node.managed?
        enqueue_ingress_reconcile!(previous_environment)
        enqueue_ingress_reconcile!(environment)

        Result.new(node:, environment:, previous_environment:)
      end
    rescue NodeBundles::Claim::Error => error
      raise Error, error.message
    end

    private

    attr_reader :node, :environment, :issuer, :broker, :on_progress
    attr_reader :publish_assignment_state

    def enqueue_ingress_reconcile!(target_environment)
      return if target_environment.blank?

      EnvironmentIngresses::ReconcileJob.perform_later(target_environment.id)
    end

    def validate_assignment_scope!
      target_organization_id = environment.project.organization_id

      if node.organization_id.present? && node.organization_id != target_organization_id
        raise Error, "node belongs to a different organization"
      end

      if !node.managed? && node.organization_id.blank?
        raise Error, "customer-managed node must belong to the target organization"
      end
    end

    def ensure_organization_bundle!
      organization = environment.project.organization
      OrganizationBundles::Claim.new(organization:, broker:).call
    end

    def ensure_environment_bundle!
      EnvironmentBundles::Claim.new(environment:, broker:).call
    end

    def assignment_satisfied?
      node.environment_id == environment.id && node.assignment_ready?
    end

    def recoverable_partial_assignment?
      node.environment_id == environment.id && node.node_bundle_id.present?
    end

    def repair_partial_assignment!
      return unless publish_assignment_state

      result = Nodes::DesiredStatePublisher.new(node:).call
      raise Error, "desired state publish failed" if result.uri.blank?
    end
  end
end
