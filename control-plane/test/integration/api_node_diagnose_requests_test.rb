# frozen_string_literal: true

require "json"
require "test_helper"

class ApiNodeDiagnoseRequestsTest < ActionDispatch::IntegrationTest
  test "owner can create, agent can complete, and owner can read a node diagnose request" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    node, access_token, = issue_test_node!(organization: organization, name: "node-a")

    post "/api/v1/cli/nodes/#{node.id}/diagnose_requests",
      headers: auth_headers_for(user),
      as: :json

    assert_response :accepted
    request_id = json_body.fetch("id")
    assert_equal "pending", json_body.fetch("status")

    post "/api/v1/agent/diagnose_requests/claim",
      headers: { "Authorization" => "Bearer #{access_token}" },
      as: :json

    assert_response :success
    assert_equal request_id, json_body.fetch("id")

    post "/api/v1/agent/diagnose_requests/#{request_id}/result",
      params: {
        result: {
          collected_at: "2026-03-29T20:05:00Z",
          agent_version: "devopsellence-agent/dev",
          summary: {
            status: "ok",
            total: 1,
            running: 1,
            stopped: 0,
            unhealthy: 0,
            logs_included: 0
          },
          containers: [
            {
              name: "devopsellence-web",
              service: "web",
              image: "docker.io/library/nginx:latest",
              running: true,
              health: "healthy",
              has_healthcheck: true,
              publish_host_port: false,
              network_ips: { "devopsellence" => "172.18.0.10" }
            }
          ]
        }
      },
      headers: { "Authorization" => "Bearer #{access_token}" },
      as: :json

    assert_response :accepted
    assert_equal "completed", json_body.fetch("status")

    get "/api/v1/cli/node_diagnose_requests/#{request_id}",
      headers: auth_headers_for(user),
      as: :json

    assert_response :success
    assert_equal "completed", json_body.fetch("status")
    assert_equal node.id, json_body.dig("node", "id")
    assert_equal "ok", json_body.dig("result", "summary", "status")
    assert_equal "web", json_body.dig("result", "containers", 0, "service")
  end

  test "agent claim returns no content when there is no pending diagnose request" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    node, access_token, = issue_test_node!(organization: organization, name: "node-a")

    post "/api/v1/agent/diagnose_requests/claim",
      headers: { "Authorization" => "Bearer #{access_token}" },
      as: :json

    assert_response :no_content
  end

  test "repeat create reuses the active diagnose request" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    node, = issue_test_node!(organization: organization, name: "node-a")

    publisher = mock("publisher")
    Nodes::DiagnoseSignalPublisher.expects(:new).with(node: node).once.returns(publisher)
    publisher.expects(:call).once

    post "/api/v1/cli/nodes/#{node.id}/diagnose_requests",
      headers: auth_headers_for(user),
      as: :json

    assert_response :accepted
    request_id = json_body.fetch("id")

    post "/api/v1/cli/nodes/#{node.id}/diagnose_requests",
      headers: auth_headers_for(user),
      as: :json

    assert_response :accepted
    assert_equal request_id, json_body.fetch("id")
    assert_equal 1, node.node_diagnose_requests.count
  end

  test "agent cannot complete a diagnose request before claiming it" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    node, access_token, = issue_test_node!(organization: organization, name: "node-a")
    request = NodeDiagnoseRequest.create_pending!(node: node, requested_by_user: user)

    post "/api/v1/agent/diagnose_requests/#{request.id}/result",
      params: {
        result: {
          collected_at: "2026-03-29T20:05:00Z",
          agent_version: "devopsellence-agent/dev",
          summary: { status: "ok", total: 0, running: 0, stopped: 0, unhealthy: 0, logs_included: 0 },
          containers: []
        }
      },
      headers: { "Authorization" => "Bearer #{access_token}" },
      as: :json

    assert_response :unprocessable_entity
    assert_equal "diagnose request must be claimed first", json_body.fetch("error_description")
    assert_equal NodeDiagnoseRequest::STATUS_PENDING, request.reload.status
  end

  private

    def auth_headers_for(user)
      _record, access_token, _refresh_token = ApiToken.issue!(user: user)
      { "Authorization" => "Bearer #{access_token}" }
    end

    def json_body
      JSON.parse(response.body)
    end
end
