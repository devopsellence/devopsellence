# frozen_string_literal: true

require "test_helper"

class EnvironmentSecretTest < ActiveSupport::TestCase
  test "builds deterministic gsm secret refs" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    ensure_test_environment_bundle!(environment)

    secret = environment.environment_secrets.create!(service_name: "web", name: "SECRET_KEY_BASE")

    assert_equal "env-#{environment.environment_bundle.token}-web-secret-key-base", secret.gcp_secret_name
    assert_equal "gsm://projects/gcp-proj-a/secrets/env-#{environment.environment_bundle.token}-web-secret-key-base/versions/latest", secret.secret_ref
  end

  test "normalizes service names before validation and secret naming" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    ensure_test_environment_bundle!(environment)

    secret = environment.environment_secrets.create!(service_name: " Web_API ", name: "SECRET_KEY_BASE")

    assert_equal "web-api", secret.service_name
    assert_equal "env-#{environment.environment_bundle.token}-web-api-secret-key-base", secret.gcp_secret_name
  end

  test "access_verified_for? requires same grantee and recent verification" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    ensure_test_organization_runtime!(organization)
    project = organization.projects.create!(name: "Project A")
    environment = project.environments.create!(
      name: "Production",
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: "pool-a",
      workload_identity_provider: "provider-a"
    )
    ensure_test_environment_bundle!(environment)
    secret = environment.environment_secrets.create!(
      service_name: "web",
      name: "SECRET_KEY_BASE",
      access_grantee_email: environment.service_account_email,
      access_verified_at: 2.hours.ago
    )

    assert_equal true, secret.access_verified_for?(environment.service_account_email)
    assert_equal false, secret.access_verified_for?("other@gcp-proj-a.iam.gserviceaccount.com")
    secret.update!(access_verified_at: 2.days.ago)
    assert_equal false, secret.access_verified_for?(environment.service_account_email)
  end

  test "builds standalone control-plane secret refs" do
    with_env(
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
    ) do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(name: "Production")
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

      secret = environment.environment_secrets.create!(service_name: "web", name: "SECRET_KEY_BASE")

      assert_equal "env-#{environment.environment_bundle.token}-web-secret-key-base", secret.gcp_secret_name
      assert_equal "https://control.example.test/api/v1/agent/secrets/environment_secrets/#{secret.id}", secret.secret_ref
    end
  end

  test "stores standalone secret values encrypted in the database" do
    with_env(
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test"
    ) do
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      project = organization.projects.create!(name: "Project A")
      environment = project.environments.create!(name: "Production")
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

      secret = environment.environment_secrets.create!(service_name: "web", name: "SECRET_KEY_BASE", value: "super-secret")

      assert_equal "super-secret", secret.reload.value
      assert_not_nil secret.attributes_before_type_cast["value"]
      assert_not_equal "super-secret", secret.attributes_before_type_cast["value"]
    end
  end
end
