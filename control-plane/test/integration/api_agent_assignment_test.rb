# frozen_string_literal: true

require "json"
require "test_helper"

class ApiAgentAssignmentTest < ActionDispatch::IntegrationTest
  test "assignment endpoint returns unassigned state for unassigned nodes" do
    node, access_token, _refresh_token = issue_test_node!(organization: nil, name: "node-a")

    get "/api/v1/agent/assignment", headers: { "Authorization" => "Bearer #{access_token}" }, as: :json

    assert_response :success
    assert_equal "unassigned", json_body["mode"]
    assert_equal node.id, json_body.dig("desired_state", "node_id")
    payload = JSON.parse(json_body.dig("desired_state", "payload_json"))
    assert_equal [], payload.fetch("environments")
  end

  test "assignment endpoint returns assigned state for nodes with bundles" do
    runtime = RuntimeProject.default!
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(name: "production")

    org_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      claimed_by_organization: organization,
      status: OrganizationBundle::STATUS_CLAIMED
    )
    env_bundle = EnvironmentBundle.create!(
      runtime_project: runtime,
      organization_bundle: org_bundle,
      claimed_by_environment: environment,
      status: EnvironmentBundle::STATUS_CLAIMED
    )
    node_bundle = NodeBundle.create!(
      runtime_project: runtime,
      organization_bundle: org_bundle,
      environment_bundle: env_bundle,
      status: NodeBundle::STATUS_CLAIMED
    )

    node, access_token, _refresh_token = issue_test_node!(organization:, name: "node-a")
    node.update!(
      environment: environment,
      node_bundle: node_bundle,
      desired_state_bucket: "bucket-a",
      desired_state_object_path: "nodes/#{node.id}/desired_state.json",
      desired_state_sequence: 1
    )

    get "/api/v1/agent/assignment", headers: { "Authorization" => "Bearer #{access_token}" }, as: :json

    assert_response :success
    assert_equal "assigned", json_body["mode"]
    assert_equal "gs://bucket-a/nodes/#{node.id}/desired_state.json", json_body["desired_state_uri"]
    assert_equal 1, json_body["desired_state_sequence"]
  end

  test "assignment endpoint returns pointer uri for pointer-capable agents" do
    runtime = RuntimeProject.default!
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(name: "production")

    org_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      claimed_by_organization: organization,
      status: OrganizationBundle::STATUS_CLAIMED
    )
    env_bundle = EnvironmentBundle.create!(
      runtime_project: runtime,
      organization_bundle: org_bundle,
      claimed_by_environment: environment,
      status: EnvironmentBundle::STATUS_CLAIMED
    )
    node_bundle = NodeBundle.create!(
      runtime_project: runtime,
      organization_bundle: org_bundle,
      environment_bundle: env_bundle,
      status: NodeBundle::STATUS_CLAIMED
    )

    node, access_token, _refresh_token = issue_test_node!(organization:, name: "node-a")
    node.update!(
      environment: environment,
      node_bundle: node_bundle,
      desired_state_bucket: "bucket-a",
      desired_state_object_path: "nodes/#{node.id}/desired_state.json",
      desired_state_sequence: 1
    )

    get "/api/v1/agent/assignment",
      headers: {
        "Authorization" => "Bearer #{access_token}",
        "devopsellence-agent-capabilities" => Nodes::DesiredStatePointer::CAPABILITY
      },
      as: :json

    assert_response :success
    assert_equal "assigned", json_body["mode"]
    assert_equal "gs://bucket-a/nodes/#{node.id}/desired_state_pointer.json", json_body["desired_state_uri"]
    assert_equal 1, json_body["desired_state_sequence"]
  end

  test "assignment endpoint does not expose assigned target before first desired state publish" do
    runtime = RuntimeProject.default!
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(name: "production")

    org_bundle = OrganizationBundle.create!(
      runtime_project: runtime,
      claimed_by_organization: organization,
      status: OrganizationBundle::STATUS_CLAIMED
    )
    env_bundle = EnvironmentBundle.create!(
      runtime_project: runtime,
      organization_bundle: org_bundle,
      claimed_by_environment: environment,
      status: EnvironmentBundle::STATUS_CLAIMED
    )
    node_bundle = NodeBundle.create!(
      runtime_project: runtime,
      organization_bundle: org_bundle,
      environment_bundle: env_bundle,
      status: NodeBundle::STATUS_CLAIMED
    )

    node, access_token, _refresh_token = issue_test_node!(organization:, name: "node-b")
    node.update!(
      environment: environment,
      node_bundle: node_bundle,
      desired_state_bucket: "bucket-a",
      desired_state_object_path: "nodes/#{node.id}/desired_state.json",
      desired_state_sequence: 0
    )

    get "/api/v1/agent/assignment", headers: { "Authorization" => "Bearer #{access_token}" }, as: :json

    assert_response :success
    assert_equal "unassigned", json_body["mode"]
  end

  def json_body
    JSON.parse(response.body)
  end
end
