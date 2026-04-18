# frozen_string_literal: true

require "test_helper"

class AbuseControlsTest < ActionDispatch::IntegrationTest
  FakeArtifact = Struct.new(:url, :filename, keyword_init: true)

  class DownloadFetcher
    def initialize(result:)
      @result = result
    end

    def fetch(version:, os:, arch:)
      @result
    end
  end

  class ChecksumFetcher
    def initialize(result:)
      @result = result
    end

    def fetch_checksums(version:)
      @result
    end
  end

  test "rate limits deploy target creation per authenticated user" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)

    with_successful_organization_runtime_provisioning do
      Gcp::EnvironmentRuntimeProvisioner.any_instance.stubs(:call).returns(
        Gcp::EnvironmentRuntimeProvisioner::Result.new(status: :ready, message: nil)
      )

      10.times do
        post "/api/v1/cli/deploy_target",
          params: {
            organization: "acme",
            project: "shop-app",
            environment: "production"
          },
          headers: auth_headers_for(user),
          as: :json

        assert_response :success
      end

      post "/api/v1/cli/deploy_target",
        params: {
          organization: "acme",
          project: "shop-app",
          environment: "production"
        },
        headers: auth_headers_for(user),
        as: :json
    end

    assert_response :too_many_requests
    assert_equal "too many requests", json_body.fetch("error_description")
  end

  test "rate limits GAR push auth issuance per authenticated user" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    project = create_owned_project_for(user)

    fake_broker = mock("broker")
    fake_broker.stubs(:issue_gar_push_auth!).returns(
      Runtime::Broker::LocalClient::PushAuth.new(
        registry_host: project.organization.gar_repository_path.split("/").first,
        gar_repository_path: project.organization.gar_repository_path,
        docker_username: "oauth2accesstoken",
        docker_password: "ya29.fake",
        expires_in: 1200
      )
    )
    Runtime::Broker.stubs(:current).returns(fake_broker)

    20.times do
      post "/api/v1/cli/projects/#{project.id}/gar/push_auth",
        params: { image_repository: "shop-app" },
        headers: auth_headers_for(user),
        as: :json

      assert_response :created
    end

    post "/api/v1/cli/projects/#{project.id}/gar/push_auth",
      params: { image_repository: "shop-app" },
      headers: auth_headers_for(user),
      as: :json

    assert_response :too_many_requests
    assert_equal "too many requests", json_body.fetch("error_description")
  end

  test "rate limits token creation per authenticated user" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    _record, access_token, refresh_token = ApiToken.issue!(user: user)
    headers = { "Authorization" => "Bearer #{access_token}" }

    10.times do
      post "/api/v1/cli/tokens",
        params: { name: "deploy", refresh_token: refresh_token },
        headers: headers,
        as: :json

      assert_response :created
    end

    post "/api/v1/cli/tokens",
      params: { name: "deploy", refresh_token: refresh_token },
      headers: headers,
      as: :json

    assert_response :too_many_requests
    assert_equal "too many requests", json_body.fetch("error_description")
  end

  test "rejects token creation without matching refresh token" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    _record, access_token, _refresh_token = ApiToken.issue!(user: user)

    post "/api/v1/cli/tokens",
      params: { name: "deploy", refresh_token: "wrong" },
      headers: { "Authorization" => "Bearer #{access_token}" },
      as: :json

    assert_response :unauthorized
    assert_equal "invalid_grant", json_body.fetch("error")
    assert_equal "invalid refresh_token", json_body.fetch("error_description")
  end

  test "rate limits environment secret writes per authenticated user" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    environment = create_owned_environment_for(user)

    Gcp::EnvironmentSecretManager.any_instance.stubs(:upsert!)
      .with do |environment_secret:, value:|
        environment_secret.save!
        value.present?
      end
      .returns(true)

    30.times do |index|
      post "/api/v1/cli/environments/#{environment.id}/secrets",
        params: {
          service_name: "web",
          name: "SECRET_#{index}",
          value: "value-#{index}"
        },
        headers: auth_headers_for(user),
        as: :json

      assert_response :created
    end

    post "/api/v1/cli/environments/#{environment.id}/secrets",
      params: {
        service_name: "web",
        name: "SECRET_LIMITED",
        value: "value"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :too_many_requests
    assert_equal "too many requests", json_body.fetch("error_description")
  end

  test "rate limits release creation per authenticated user" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    project = create_owned_project_for(user)

    20.times do |index|
      post "/api/v1/cli/projects/#{project.id}/releases",
        params: release_params(index),
        headers: auth_headers_for(user),
        as: :json

      assert_response :created
    end

    post "/api/v1/cli/projects/#{project.id}/releases",
      params: release_params(21),
      headers: auth_headers_for(user),
      as: :json

    assert_response :too_many_requests
    assert_equal "too many requests", json_body.fetch("error_description")
  end

  test "rate limits release publish per authenticated user" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    environment = create_owned_environment_for(user)
    release = environment.project.releases.create!(
      git_sha: "a" * 40,
      revision: "rev-1",
      image_repository: "shop-app",
      image_digest: "sha256:#{'b' * 64}",
      runtime_json: release_runtime_json
    )
    deployment = environment.deployments.create!(
      release: release,
      sequence: 1,
      request_token: SecureRandom.hex(8),
      status: Deployment::STATUS_SCHEDULING,
      status_message: "waiting to publish desired state",
      published_at: Time.current
    )

    Deployments::Scheduler.any_instance.stubs(:call).returns(Struct.new(:deployment).new(deployment))

    20.times do |index|
      post "/api/v1/cli/releases/#{release.id}/publish",
        params: {
          environment_id: environment.id,
          request_token: "req-#{index}"
        },
        headers: auth_headers_for(user),
        as: :json

      assert_response :created
    end

    post "/api/v1/cli/releases/#{release.id}/publish",
      params: {
        environment_id: environment.id,
        request_token: "req-limited"
      },
      headers: auth_headers_for(user),
      as: :json

    assert_response :too_many_requests
    assert_equal "too many requests", json_body.fetch("error_description")
  end

  test "rate limits public artifact downloads per ip across binaries and checksums" do
    remote_addr = { "REMOTE_ADDR" => "203.0.113.10" }
    cli_fetcher = DownloadFetcher.new(result: FakeArtifact.new(url: "https://example.test/devopsellence", filename: "devopsellence"))
    checksum_fetcher = ChecksumFetcher.new(result: FakeArtifact.new(url: "https://example.test/SHA256SUMS", filename: "SHA256SUMS"))

    with_cli_release_fetcher(cli_fetcher) do
      with_agent_release_fetcher(checksum_fetcher) do
        60.times do
          get cli_download_path, params: { version: "v0.1.0", os: "linux", arch: "amd64" }, headers: remote_addr
          assert_response :redirect
        end

        get agent_checksums_path, params: { version: "v0.1.0" }, headers: remote_addr
      end
    end

    assert_response :too_many_requests
    assert_equal "too many requests", response.body
  end

  private

  def auth_headers_for(user)
    _record, access_token, _refresh_token = ApiToken.issue!(user: user)
    { "Authorization" => "Bearer #{access_token}" }
  end

  def create_owned_project_for(user)
    organization = Organization.create!(
      name: "acme-#{SecureRandom.hex(3)}",
      gcp_project_id: "runtime-proj",
      gcp_project_number: "123456789",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a",
      gar_repository_region: "us-east1",
      gar_repository_name: "org-#{SecureRandom.hex(3)}-apps",
      gcs_bucket_name: "devopsellence-acme-#{SecureRandom.hex(3)}"
    )
    OrganizationMembership.create!(organization: organization, user: user, role: OrganizationMembership::ROLE_OWNER)
    organization.projects.create!(name: "ShopApp")
  end

  def create_owned_environment_for(user)
    project = create_owned_project_for(user)
    project.environments.create!(
      name: "production",
      gcp_project_id: project.organization.gcp_project_id,
      gcp_project_number: project.organization.gcp_project_number,
      workload_identity_pool: project.organization.workload_identity_pool,
      workload_identity_provider: project.organization.workload_identity_provider,
      service_account_email: "env@runtime-proj.iam.gserviceaccount.com"
    )
  end

  def release_params(index)
    {
      git_sha: SecureRandom.hex(20),
      image_repository: "shop-app",
      image_digest: "sha256:#{SecureRandom.hex(32)}",
      revision: "rev-#{index}",
      services: {
        web: web_service_runtime(port: 80)
      },
      ingress_service: "web"
    }
  end

  def json_body
    JSON.parse(response.body)
  end
end
