# frozen_string_literal: true

require "test_helper"

module Deployments
  class ProgressRecorderTest < ActiveSupport::TestCase
    include ActiveJob::TestHelper

    test "release command success marks deployment succeeded and enqueues publish" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "Production",
        gcp_project_id: "gcp-proj-a",
        gcp_project_number: "123456789",
        service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
        workload_identity_pool: "pool-a",
        workload_identity_provider: "provider-a",
        runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
      )
      release = project.releases.create!(
        git_sha: "abcd1234",
        image_digest: "sha256:abc",
        image_repository: "api",
        web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json,
        release_command: "bundle exec rails db:migrate",
        revision: "rel-1"
      )
      node, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(environment: environment)
      deployment = environment.deployments.create!(
        release: release,
        sequence: 1,
        request_token: SecureRandom.hex(16),
        status: Deployment::STATUS_ROLLING_OUT,
        status_message: "running release command",
        published_at: Time.current,
        release_command_status: Deployment::RELEASE_COMMAND_STATUS_PENDING,
        release_command_node: node
      )
      deployment.deployment_node_statuses.create!(
        node: node,
        phase: DeploymentNodeStatus::PHASE_PENDING,
        message: "waiting to run release command"
      )

      assert_enqueued_with(job: Deployments::PublishJob, args: [deployment.id]) do
        ProgressRecorder.new(
          node: node,
          status: {
            revision: release.revision,
            phase: DeploymentNodeStatus::PHASE_SETTLED,
            message: "release command completed",
            task: {
              name: "release_command",
              phase: DeploymentNodeStatus::PHASE_SETTLED,
              message: "release command completed"
            }
          }
        ).call
      end

      deployment.reload
      assert_equal Deployment::RELEASE_COMMAND_STATUS_SUCCEEDED, deployment.release_command_status
      assert_equal Deployment::STATUS_ROLLING_OUT, deployment.status
      assert_equal "publishing desired state", deployment.status_message
    end
  end
end
