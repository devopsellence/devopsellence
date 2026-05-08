# frozen_string_literal: true

require "test_helper"

class EnvironmentIngresses::EligibleNodesTest < ActiveSupport::TestCase
  test "uses one bulk deployment status query for candidate nodes" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com",
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES,
      ingress_strategy: Environment::INGRESS_STRATEGY_DIRECT_DNS
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      image_digest: "sha256:#{"b" * 64}",
      image_repository: "api",
      runtime_json: release_runtime_json,
      revision: "rel-1"
    )
    environment.update!(current_release: release)

    settled_node = create_ingress_node!(organization:, environment:, name: "node-a", public_ip: "198.51.100.10")
    failed_node = create_ingress_node!(organization:, environment:, name: "node-b", public_ip: "198.51.100.11")
    create_ingress_node!(organization:, environment:, name: "node-c", public_ip: "198.51.100.12")
    old_deployment = create_deployment!(environment:, release:, sequence: 1)
    current_deployment = create_deployment!(environment:, release:, sequence: 2)
    create_node_status!(deployment: old_deployment, node: settled_node, phase: DeploymentNodeStatus::PHASE_ERROR)
    create_node_status!(deployment: old_deployment, node: failed_node, phase: DeploymentNodeStatus::PHASE_SETTLED)
    create_node_status!(deployment: current_deployment, node: settled_node, phase: DeploymentNodeStatus::PHASE_SETTLED)
    create_node_status!(deployment: current_deployment, node: failed_node, phase: DeploymentNodeStatus::PHASE_ERROR)

    result = nil
    status_queries = count_deployment_node_status_queries do
      result = EnvironmentIngresses::EligibleNodes.new(environment: environment.reload).call
    end

    assert_equal [ settled_node ], result
    assert_equal 1, status_queries
  end

  private

  def create_ingress_node!(organization:, environment:, name:, public_ip:)
    node, = issue_test_node!(organization:, name:, public_ip:)
    node.update!(environment:)
    node
  end

  def create_deployment!(environment:, release:, sequence:)
    environment.deployments.create!(
      release:,
      sequence:,
      request_token: SecureRandom.hex(8),
      status: Deployment::STATUS_PUBLISHED,
      status_message: "rollout settled",
      published_at: Time.current,
      finished_at: Time.current
    )
  end

  def create_node_status!(deployment:, node:, phase:)
    deployment.deployment_node_statuses.create!(
      node:,
      phase:,
      message: phase,
      reported_at: Time.current
    )
  end

  def count_deployment_node_status_queries(&block)
    count = 0
    subscriber = lambda do |_name, _started, _finished, _id, payload|
      sql = payload[:sql].to_s
      count += 1 if payload[:name] != "SCHEMA" && !payload[:cached] && sql.match?(/\bdeployment_node_statuses\b/i)
    end

    ActiveSupport::Notifications.subscribed(subscriber, "sql.active_record", &block)
    count
  end
end
