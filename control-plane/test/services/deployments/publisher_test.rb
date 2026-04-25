# frozen_string_literal: true

require "securerandom"
require "test_helper"

module Deployments
  class PublisherTest < ActiveSupport::TestCase
    test "ingress is not ready when bundled ingress is missing configured release hosts" do
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
      bundle = ensure_test_environment_bundle!(environment)
      configured_host = "#{SecureRandom.alphanumeric(6).downcase}.devopsellence.io"
      release = project.releases.create!(
        git_sha: "a" * 40,
        revision: "rel-1",
        image_repository: "shop-app",
        image_digest: "sha256:#{"b" * 64}",
        runtime_json: release_runtime_json(ingress: {
          "hosts" => [ configured_host ],
          "rules" => [
            {
              "match" => { "host" => configured_host, "path_prefix" => "/" },
              "target" => { "service" => "web", "port" => "http" }
            }
          ]
        })
      )
      ingress = environment.create_environment_ingress!(
        hostname: bundle.hostname,
        cloudflare_tunnel_id: bundle.cloudflare_tunnel_id,
        gcp_secret_name: bundle.gcp_secret_name,
        status: EnvironmentIngress::STATUS_READY,
        provisioned_at: bundle.provisioned_at
      )
      assert_equal [ bundle.hostname ], ingress.hosts

      publisher = Publisher.new(environment:, release:)

      assert_not publisher.send(:ingress_ready?)
      ingress.assign_hosts!([ bundle.hostname, configured_host ])
      assert publisher.send(:ingress_ready?)
    end
  end
end
