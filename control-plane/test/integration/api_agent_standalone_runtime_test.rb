# frozen_string_literal: true

require "test_helper"
require "json"
require "uri"

class ApiAgentStandaloneRuntimeTest < ActionDispatch::IntegrationTest
  test "desired state endpoint serves standalone published envelope with etag support" do
    with_env(
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
    ) do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      project = organization.projects.create!(name: "Project A")
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
      environment.update!(runtime_project: runtime, environment_bundle:, service_account_email: environment_bundle.service_account_email)
      node_bundle = NodeBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_CLAIMED
      )
      node, access_token, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(environment:, node_bundle:)

      result = Nodes::DesiredStatePublisher.new(
        node: node,
        payload: ->(sequence:) { { schemaVersion: 2, revision: "rev-#{sequence}", environments: [] } }
      ).call

      assert_equal "https://control.example.test/api/v1/agent/desired_state", result.uri
      assert_equal 1, node.reload.desired_state_sequence

      get "/api/v1/agent/desired_state", headers: { "Authorization" => "Bearer #{access_token}" }, as: :json
      assert_response :success
      assert_equal "signed_desired_state.v1", json_body.fetch("format")
      etag = response.headers.fetch("ETag")

      get "/api/v1/agent/desired_state", headers: {
        "Authorization" => "Bearer #{access_token}",
        "If-None-Match" => etag
      }, as: :json
      assert_response :not_modified
    end
  end

  test "custom standalone payloads are normalized to desired state v2" do
    with_env(
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
    ) do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      project = organization.projects.create!(name: "Project A")
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
      environment.update!(runtime_project: runtime, environment_bundle:, service_account_email: environment_bundle.service_account_email)
      node_bundle = NodeBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_CLAIMED
      )
      node, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(environment:, node_bundle:)

      result = Nodes::DesiredStatePublisher.new(
        node: node,
        payload: ->(sequence:) { { revision: "rev-#{sequence}" } }
      ).call

      payload = JSON.parse(result.payload.fetch(:payload_json))
      assert_equal 2, payload.fetch("schemaVersion")
      assert_equal [], payload.fetch("environments")
      assert_equal "rev-1", payload.fetch("revision")
    end
  end

  test "secret and registry auth endpoints serve standalone runtime data" do
    with_env(
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
    ) do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      organization.create_organization_registry_config!(
        registry_host: "ghcr.io",
        repository_namespace: "acme/apps",
        username: "robot",
        password: "reg-secret"
      )
      project = organization.projects.create!(name: "Project A")
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
      environment.update!(runtime_project: runtime, environment_bundle:, service_account_email: environment_bundle.service_account_email)
      node_bundle = NodeBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_CLAIMED
      )
      node, access_token, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(environment:, node_bundle:)

      secret = environment.environment_secrets.find_or_initialize_by(service_name: "web", name: "SECRET_KEY_BASE")
      Gcp::EnvironmentSecretManager.new.upsert!(environment_secret: secret, value: "super-secret")

      secret_uri = URI.parse(secret.reload.secret_ref)
      get secret_uri.request_uri, headers: { "Authorization" => "Bearer #{access_token}" }, as: :json
      assert_response :success
      assert_equal "super-secret", json_body.fetch("value")

      post "/api/v1/agent/registry_auth",
        params: { image: "ghcr.io/acme/apps/web:rev-1" },
        headers: { "Authorization" => "Bearer #{access_token}" },
        as: :json
      assert_response :success
      assert_equal "ghcr.io", json_body.fetch("server_address")
      assert_equal "robot", json_body.fetch("username")
      assert_equal "reg-secret", json_body.fetch("password")
    end
  end

  test "tunnel token endpoint serves standalone environment bundle tunnel token" do
    with_env(
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
    ) do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      project = organization.projects.create!(name: "Project A")
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
      environment_bundle.update!(tunnel_token: "tunnel-secret-token")
      environment.update!(runtime_project: runtime, environment_bundle:, service_account_email: environment_bundle.service_account_email)
      node_bundle = NodeBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_CLAIMED
      )
      node, access_token, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(environment:, node_bundle:)

      get "/api/v1/agent/secrets/environment_bundles/#{environment_bundle.id}/tunnel_token",
        headers: { "Authorization" => "Bearer #{access_token}" }, as: :json
      assert_response :success
      assert_equal "tunnel-secret-token", json_body.fetch("value")
    end
  end

  test "secret endpoint returns forbidden when node requests secret from different environment" do
    with_env(
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
    ) do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      project = organization.projects.create!(name: "Project A")
      env_a = project.environments.create!(name: "production")
      env_b = project.environments.create!(name: "staging")
      runtime = RuntimeProject.default!
      organization_bundle = OrganizationBundle.create!(
        runtime_project: runtime,
        claimed_by_organization: organization,
        status: OrganizationBundle::STATUS_CLAIMED
      )
      environment_bundle = EnvironmentBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        claimed_by_environment: env_a,
        status: EnvironmentBundle::STATUS_CLAIMED
      )
      env_a.update!(runtime_project: runtime, environment_bundle:, service_account_email: environment_bundle.service_account_email)
      env_b_bundle = EnvironmentBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        claimed_by_environment: env_b,
        status: EnvironmentBundle::STATUS_CLAIMED
      )
      env_b.update!(runtime_project: runtime, environment_bundle: env_b_bundle, service_account_email: env_b_bundle.service_account_email)
      node_bundle = NodeBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_CLAIMED
      )
      node, access_token, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(environment: env_a, node_bundle:)

      secret = env_b.environment_secrets.find_or_initialize_by(service_name: "web", name: "OTHER_SECRET")
      Gcp::EnvironmentSecretManager.new.upsert!(environment_secret: secret, value: "other-value")

      get "/api/v1/agent/secrets/environment_secrets/#{secret.id}",
        headers: { "Authorization" => "Bearer #{access_token}" }, as: :json
      assert_response :forbidden
      assert_equal "forbidden", json_body.fetch("error")
    end
  end

  test "tunnel token endpoint returns forbidden when node requests token from different environment bundle" do
    with_env(
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
    ) do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      project = organization.projects.create!(name: "Project A")
      env_a = project.environments.create!(name: "production")
      env_b = project.environments.create!(name: "staging")
      runtime = RuntimeProject.default!
      organization_bundle = OrganizationBundle.create!(
        runtime_project: runtime,
        claimed_by_organization: organization,
        status: OrganizationBundle::STATUS_CLAIMED
      )
      env_a_bundle = EnvironmentBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        claimed_by_environment: env_a,
        status: EnvironmentBundle::STATUS_CLAIMED
      )
      env_a.update!(runtime_project: runtime, environment_bundle: env_a_bundle, service_account_email: env_a_bundle.service_account_email)
      env_b_bundle = EnvironmentBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        claimed_by_environment: env_b,
        status: EnvironmentBundle::STATUS_CLAIMED
      )
      env_b_bundle.update!(tunnel_token: "env-b-tunnel-token")
      env_b.update!(runtime_project: runtime, environment_bundle: env_b_bundle, service_account_email: env_b_bundle.service_account_email)
      node_bundle = NodeBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        environment_bundle: env_a_bundle,
        status: NodeBundle::STATUS_CLAIMED
      )
      node, access_token, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(environment: env_a, node_bundle:)

      get "/api/v1/agent/secrets/environment_bundles/#{env_b_bundle.id}/tunnel_token",
        headers: { "Authorization" => "Bearer #{access_token}" }, as: :json
      assert_response :forbidden
      assert_equal "forbidden", json_body.fetch("error")
    end
  end

  test "old desired state documents are pruned after publishing" do
    with_env(
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
    ) do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      project = organization.projects.create!(name: "Project A")
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
      environment.update!(runtime_project: runtime, environment_bundle:, service_account_email: environment_bundle.service_account_email)
      node_bundle = NodeBundle.create!(
        runtime_project: runtime,
        organization_bundle: organization_bundle,
        environment_bundle: environment_bundle,
        status: NodeBundle::STATUS_CLAIMED
      )
      node, _access_token, = issue_test_node!(organization: organization, name: "node-a")
      node.update!(environment:, node_bundle:)

      publisher = Nodes::DesiredStatePublisher.new(
        node: node,
        payload: ->(sequence:) { { schemaVersion: 2, revision: "rev-#{sequence}", environments: [] } }
      )

      publisher.call
      assert_equal 1, StandaloneDesiredStateDocument.where(node: node).count

      publisher.call
      assert_equal [1, 2], StandaloneDesiredStateDocument.where(node: node).order(:sequence).pluck(:sequence)

      publisher.call
      assert_equal [2, 3], StandaloneDesiredStateDocument.where(node: node).order(:sequence).pluck(:sequence)
    end
  end

  private

  def json_body
    JSON.parse(response.body)
  end
end
