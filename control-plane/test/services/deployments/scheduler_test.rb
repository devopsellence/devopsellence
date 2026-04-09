# frozen_string_literal: true

require "securerandom"
require "test_helper"

class DeploymentsSchedulerTest < ActiveSupport::TestCase
  include ActiveJob::TestHelper
  POOL_A = "projects/123456789/locations/global/workloadIdentityPools/pool-a"
  PROVIDER_A = "#{POOL_A}/providers/provider-a"

  test "schedules deployment and enqueues publish job once" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: POOL_A,
      workload_identity_provider: PROVIDER_A,
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rel-1",
      image_repository: "api",
      image_digest: "sha256:#{'b' * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )

    result = nil
    assert_enqueued_jobs 1, only: Deployments::PublishJob do
      result = Deployments::Scheduler.new(
        environment: environment,
        release: release,
        request_token: "req-1"
      ).call
    end

    assert_equal true, result.scheduled
    assert_equal Deployment::STATUS_SCHEDULING, result.deployment.status
    assert_equal "waiting to publish desired state", result.deployment.status_message
  end

  test "reuses existing deployment for the same request token" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: POOL_A,
      workload_identity_provider: PROVIDER_A,
      runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
    )
    release = project.releases.create!(
      git_sha: "a" * 40,
      revision: "rel-1",
      image_repository: "api",
      image_digest: "sha256:#{'b' * 64}",
      web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
    )

    first = nil
    second = nil
    assert_enqueued_jobs 1, only: Deployments::PublishJob do
      first = Deployments::Scheduler.new(environment: environment, release: release, request_token: "req-1").call
      second = Deployments::Scheduler.new(environment: environment, release: release, request_token: "req-1").call
    end

    assert_equal true, first.scheduled
    assert_equal false, second.scheduled
    assert_equal first.deployment.id, second.deployment.id
  end
end
