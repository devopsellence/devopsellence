# frozen_string_literal: true

require "test_helper"

module Deployments
  class RecoverStaleSchedulingsJobTest < ActiveJob::TestCase
    include ActiveSupport::Testing::TimeHelpers

    test "re-enqueues stale scheduling deployments" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: "gcp-proj-a",
        gcp_project_number: "123456789",
        service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
        workload_identity_pool: "pool-a",
        workload_identity_provider: "provider-a",
        runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
      )
      release = project.releases.create!(
        git_sha: "a" * 40,
        revision: "rel-1",
        image_repository: "api",
        image_digest: "sha256:#{'b' * 64}",
        runtime_json: release_runtime_json
      )
      deployment = environment.deployments.create!(
        release: release,
        sequence: 1,
        request_token: "req-1",
        status: Deployment::STATUS_SCHEDULING,
        status_message: "waiting for managed capacity",
        published_at: Time.current
      )
      deployment.update_columns(updated_at: 3.minutes.ago)

      assert_enqueued_with(job: Deployments::PublishJob, args: [ deployment.id ]) do
        RecoverStaleSchedulingsJob.perform_now(now: Time.current)
      end

      assert_equal "retrying stalled deployment scheduling", deployment.reload.status_message
    end

    test "ignores fresh scheduling deployments" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: "gcp-proj-a",
        gcp_project_number: "123456789",
        service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
        workload_identity_pool: "pool-a",
        workload_identity_provider: "provider-a",
        runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
      )
      release = project.releases.create!(
        git_sha: "a" * 40,
        revision: "rel-2",
        image_repository: "api",
        image_digest: "sha256:#{'c' * 64}",
        runtime_json: release_runtime_json
      )
      environment.deployments.create!(
        release: release,
        sequence: 1,
        request_token: "req-2",
        status: Deployment::STATUS_SCHEDULING,
        status_message: "waiting to publish desired state",
        published_at: Time.current
      )

      assert_no_enqueued_jobs only: Deployments::PublishJob do
        RecoverStaleSchedulingsJob.perform_now(now: Time.current)
      end
    end
  end
end
