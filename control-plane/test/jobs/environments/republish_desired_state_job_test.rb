# frozen_string_literal: true

require "test_helper"

module Environments
  class RepublishDesiredStateJobTest < ActiveJob::TestCase
    test "republishes desired state for assigned nodes when a current release exists" do
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
        image_digest: "sha256:#{"b" * 64}",
        web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
      )
      environment.update!(current_release: release)
      node, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(
        environment: environment,
        desired_state_bucket: organization.gcs_bucket_name,
        desired_state_object_path: "nodes/#{SecureRandom.hex(4)}/desired_state.json"
      )

      publisher = mock("publisher")
      Nodes::DesiredStatePublisher.expects(:new).with(node:, release: release).returns(publisher)
      publisher.expects(:call).once

      Environments::RepublishDesiredStateJob.perform_now(environment.id)
    end

    test "does nothing when the environment has no current release" do
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

      Nodes::DesiredStatePublisher.expects(:new).never

      Environments::RepublishDesiredStateJob.perform_now(environment.id)
    end

    test "republishes standalone desired state even when desired_state_bucket is blank" do
      with_env(
        "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
        "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
      ) do
        organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
        project = organization.projects.create!(name: "ShopApp")
        environment = project.environments.create!(name: "production")
        runtime = RuntimeProject.default!
        organization_bundle = OrganizationBundle.create!(
          runtime_project: runtime,
          claimed_by_organization: organization,
          status: OrganizationBundle::STATUS_CLAIMED
        )
        environment_bundle = EnvironmentBundle.create!(
          runtime_project: runtime,
          organization_bundle: organization_bundle,
          claimed_by_environment: environment,
          status: EnvironmentBundle::STATUS_CLAIMED
        )
        environment.update!(
          runtime_project: runtime,
          environment_bundle: environment_bundle,
          service_account_email: environment_bundle.service_account_email
        )
        release = project.releases.create!(
          git_sha: "a" * 40,
          revision: "rev-1",
          image_repository: "shop-app",
          image_digest: "sha256:#{"b" * 64}",
          web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
        )
        environment.update!(current_release: release)
        node_bundle = NodeBundle.create!(
          runtime_project: runtime,
          organization_bundle: organization_bundle,
          environment_bundle: environment_bundle,
          status: NodeBundle::STATUS_CLAIMED
        )
        node, = issue_test_node!(organization: organization, name: "node-a")
        node.update!(
          environment: environment,
          node_bundle: node_bundle,
          desired_state_bucket: "",
          desired_state_object_path: node_bundle.desired_state_object_path
        )

        publisher = mock("publisher")
        Nodes::DesiredStatePublisher.expects(:new).with(node:, release: release).returns(publisher)
        publisher.expects(:call).once

        Environments::RepublishDesiredStateJob.perform_now(environment.id)
      end
    end
  end
end
