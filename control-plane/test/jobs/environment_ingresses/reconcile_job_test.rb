# frozen_string_literal: true

require "test_helper"

module EnvironmentIngresses
  class ReconcileJobTest < ActiveJob::TestCase
    test "republishes desired state when tunnel ingress becomes ready" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: organization.gcp_project_id,
        gcp_project_number: organization.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider,
        runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
      )
      release = project.releases.create!(
        git_sha: "a" * 40,
        revision: "rel-1",
        image_repository: "shop-app",
        image_digest: "sha256:#{"b" * 64}",
        web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
      )
      environment.update!(current_release: release)
      environment.create_environment_ingress!(
        hostname: random_ingress_hostname,
        cloudflare_tunnel_id: "tunnel-1",
        gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
        status: EnvironmentIngress::STATUS_PENDING
      )
      node, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(environment: environment)

      EnvironmentIngresses::Reconciler.any_instance.stubs(:call).with do
        environment.environment_ingress.update!(status: EnvironmentIngress::STATUS_READY, provisioned_at: Time.current)
        environment.association(:environment_ingress).reset
        true
      end.returns(environment.environment_ingress)

      assert_enqueued_with(job: Environments::RepublishDesiredStateJob, args: [environment.id]) do
        ReconcileJob.perform_now(environment.id)
      end
    end

    test "does not republish desired state when ingress payload is unchanged" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: organization.gcp_project_id,
        gcp_project_number: organization.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider,
        runtime_kind: Environment::RUNTIME_CUSTOMER_NODES
      )
      release = project.releases.create!(
        git_sha: "a" * 40,
        revision: "rel-1",
        image_repository: "shop-app",
        image_digest: "sha256:#{"b" * 64}",
        web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
      )
      environment.update!(current_release: release)
      environment.create_environment_ingress!(
        hostname: random_ingress_hostname,
        cloudflare_tunnel_id: "tunnel-1",
        gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
        status: EnvironmentIngress::STATUS_READY,
        provisioned_at: Time.current
      )
      node, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(environment: environment)

      EnvironmentIngresses::Reconciler.any_instance.stubs(:call).returns(environment.environment_ingress)

      assert_no_enqueued_jobs only: Environments::RepublishDesiredStateJob do
        ReconcileJob.perform_now(environment.id)
      end
    end
  end
end
