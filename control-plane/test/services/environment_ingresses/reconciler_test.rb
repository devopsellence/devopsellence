# frozen_string_literal: true

require "securerandom"
require "test_helper"

module EnvironmentIngresses
  class ReconcilerTest < ActiveSupport::TestCase
    test "passes stale hosts to the cloudflare provisioner after syncing ingress hosts" do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      ensure_test_organization_runtime!(organization)
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(
        name: "production",
        gcp_project_id: organization.gcp_project_id,
        gcp_project_number: organization.gcp_project_number,
        workload_identity_pool: organization.workload_identity_pool,
        workload_identity_provider: organization.workload_identity_provider,
        service_account_email: "env@#{organization.gcp_project_id}.iam.gserviceaccount.com"
      )
      previous_host = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"
      desired_host = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"
      release = project.releases.create!(
        git_sha: "a" * 40,
        revision: "rel-1",
        image_repository: "shop-app",
        image_digest: "sha256:#{"b" * 64}",
        runtime_json: release_runtime_json(ingress: {
          "hosts" => [ desired_host ],
          "rules" => [
            {
              "match" => { "host" => desired_host, "path_prefix" => "/" },
              "target" => { "service" => "web", "port" => "http" }
            }
          ]
        })
      )
      environment.update!(current_release: release)
      ingress = environment.create_environment_ingress!(
        hostname: previous_host,
        cloudflare_tunnel_id: "tunnel-1",
        gcp_secret_name: "env-#{environment.id}-ingress-cloudflare-tunnel-token",
        status: EnvironmentIngress::STATUS_READY,
        provisioned_at: Time.current
      )
      client = Object.new
      provisioner = mock("provisioner")

      Cloudflare::EnvironmentIngressProvisioner.expects(:new).with(
        environment: environment,
        client: client,
        release: release,
        stale_hosts: [ previous_host ]
      ).returns(provisioner)
      provisioner.expects(:call).returns(ingress)

      Reconciler.new(environment: environment, client: client, release: release).call

      assert_equal [ desired_host ], ingress.reload.hosts
    end
  end
end
