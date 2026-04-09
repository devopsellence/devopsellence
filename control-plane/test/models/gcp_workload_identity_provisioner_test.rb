# frozen_string_literal: true

require "test_helper"
require "securerandom"

class GcpWorkloadIdentityProvisionerTest < ActiveSupport::TestCase
  POOL_A = "projects/123456789/locations/global/workloadIdentityPools/pool-a"
  PROVIDER_A = "#{POOL_A}/providers/provider-a"

  test "provisions workload identity resources with injected iam client" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    workload_identity = organization.organization_workload_identities.create!(
      project: project,
      created_by_user: user,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: POOL_A,
      workload_identity_provider: PROVIDER_A,
      status: OrganizationWorkloadIdentity::STATUS_FAILED
    )

    operation = Struct.new(:done, :error).new(true, nil)
    policy = Google::Apis::IamV1::Policy.new(bindings: [], version: 1)
    iam = Object.new
    iam.stubs(:get_project_service_account).returns(true)
    iam.stubs(:get_project_location_workload_identity_pool).returns(true)
    iam.stubs(:get_project_location_workload_identity_pool_provider).returns(true)
    iam.stubs(:get_project_service_account_iam_policy).returns(policy)
    iam.stubs(:set_service_account_iam_policy).returns(true)
    iam.stubs(:create_service_account).returns(true)
    iam.stubs(:create_project_location_workload_identity_pool).returns(Struct.new(:name).new("operations/pool-a"))
    iam.stubs(:create_project_location_workload_identity_pool_provider).returns(Struct.new(:name).new("operations/provider-a"))
    iam.stubs(:get_project_location_workload_identity_pool_operation).returns(operation)

    result = Gcp::WorkloadIdentityProvisioner.new(
      workload_identity: workload_identity,
      issuer: "https://cp.example.com",
      iam: iam
    ).call

    assert_equal OrganizationWorkloadIdentity::STATUS_READY, result.status
    assert_nil result.message
  end

  test "returns failed when iam client raises an error" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")
    project = organization.projects.create!(name: "Project A")
    workload_identity = organization.organization_workload_identities.create!(
      project: project,
      created_by_user: user,
      gcp_project_id: "gcp-proj-a",
      gcp_project_number: "123456789",
      service_account_email: "svc-a@gcp-proj-a.iam.gserviceaccount.com",
      workload_identity_pool: POOL_A,
      workload_identity_provider: PROVIDER_A,
      status: OrganizationWorkloadIdentity::STATUS_FAILED
    )

    iam = Object.new
    iam.stubs(:get_project_service_account).raises(Google::Apis::ClientError.new("boom", status_code: 500))

    result = Gcp::WorkloadIdentityProvisioner.new(
      workload_identity: workload_identity,
      issuer: "https://cp.example.com",
      iam: iam
    ).call

    assert_equal OrganizationWorkloadIdentity::STATUS_FAILED, result.status
    assert_includes result.message, "boom"
  end
end
