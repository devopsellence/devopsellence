# frozen_string_literal: true

require "test_helper"

module Deployments
  class PublishJobTest < ActiveJob::TestCase
    test "reruns rolling out deployment without release task" do
      deployment = build_deployment(status: Deployment::STATUS_ROLLING_OUT)

      publisher = mock
      publisher.expects(:call).once
      Deployments::Publisher.expects(:new).with(
        environment: deployment.environment,
        release: deployment.release,
        deployment:
      ).returns(publisher)

      Deployments::PublishJob.perform_now(deployment.id)
    end

    test "does not rerun rolling out deployment before release task succeeds" do
      deployment = build_deployment(
        status: Deployment::STATUS_ROLLING_OUT,
        runtime_json: release_runtime_json(tasks: {
          "release" => {
            "service" => "web",
            "command" => "bin/rails db:migrate"
          }
        }),
        release_task_status: Deployment::RELEASE_TASK_STATUS_PENDING
      )

      Deployments::Publisher.expects(:new).never

      Deployments::PublishJob.perform_now(deployment.id)
    end

    test "marks deployment failed when publisher raises" do
      deployment = build_deployment

      Deployments::Publisher.any_instance.stubs(:call).raises(StandardError, "boom")

      Deployments::PublishJob.perform_now(deployment.id)

      deployment.reload
      assert_equal Deployment::STATUS_FAILED, deployment.status
      assert_equal "publish failed", deployment.status_message
      assert_equal "boom", deployment.error_message
      assert deployment.finished_at.present?
    end

    private

    def build_deployment(status: Deployment::STATUS_SCHEDULING, runtime_json: release_runtime_json, release_task_status: nil)
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "ShopApp")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: organization.gcp_project_id,
        gcp_project_number: organization.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider,
        runtime_kind: Environment::RUNTIME_MANAGED
      )
      release = project.releases.create!(
        git_sha: "a" * 40,
        revision: "rev-1",
        image_repository: "shop-app",
        image_digest: "sha256:#{'b' * 64}",
        runtime_json:
      )
      environment.deployments.create!(
        release: release,
        sequence: 1,
        request_token: "req-1",
        status:,
        status_message: "waiting to publish desired state",
        published_at: Time.current,
        release_task_status:
      )
    end
  end
end
