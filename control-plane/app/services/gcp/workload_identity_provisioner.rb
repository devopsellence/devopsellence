# frozen_string_literal: true

require "google/apis/iam_v1"
require "googleauth"

module Gcp
  class WorkloadIdentityProvisioner
    SCOPE = Google::Apis::IamV1::AUTH_CLOUD_PLATFORM
    OPERATION_RETRIES = 30
    OPERATION_SLEEP_SECONDS = 1

    Result = Struct.new(:status, :message, keyword_init: true)

    def initialize(workload_identity:, issuer:, iam: nil)
      @workload_identity = workload_identity
      @issuer = issuer
      @iam = iam
    end

    def call
      iam = iam_service

      ensure_service_account(iam)
      ensure_workload_identity_pool(iam)
      ensure_workload_identity_provider(iam)
      bind_service_account(iam)

      Result.new(status: OrganizationWorkloadIdentity::STATUS_READY, message: nil)
    rescue Google::Apis::AuthorizationError, Google::Apis::ClientError, Google::Apis::ServerError, StandardError => error
      Result.new(status: OrganizationWorkloadIdentity::STATUS_FAILED, message: "gcp provisioning failed: #{error.message}")
    end

    private

    attr_reader :workload_identity, :issuer

    def ensure_service_account(iam)
      iam.get_project_service_account(service_account_resource_name)
    rescue Google::Apis::ClientError => error
      raise unless not_found?(error)

      request = Google::Apis::IamV1::CreateServiceAccountRequest.new(
        account_id: service_account_name,
        service_account: Google::Apis::IamV1::ServiceAccount.new(
          display_name: "devopsellence #{project_label}"
        )
      )

      iam.create_service_account("projects/#{workload_identity.gcp_project_id}", request)
    end

    def ensure_workload_identity_pool(iam)
      iam.get_project_location_workload_identity_pool(workload_identity_pool_resource_name)
    rescue Google::Apis::ClientError => error
      raise unless not_found?(error)

      pool = Google::Apis::IamV1::WorkloadIdentityPool.new(
        display_name: "devopsellence #{project_label}",
        description: "devopsellence org #{workload_identity.organization_id} project #{project_label}"
      )

      operation = iam.create_project_location_workload_identity_pool(
        workload_identity_pool_parent,
        pool,
        workload_identity_pool_id: workload_identity.workload_identity_pool
      )
      wait_for_operation!(iam, operation.name)
    end

    def ensure_workload_identity_provider(iam)
      iam.get_project_location_workload_identity_pool_provider(workload_identity_provider_resource_name)
    rescue Google::Apis::ClientError => error
      raise unless not_found?(error)

      provider = Google::Apis::IamV1::WorkloadIdentityPoolProvider.new(
        display_name: "devopsellence #{project_label}",
        attribute_mapping: {
          "google.subject" => "assertion.sub",
          "attribute.organization_id" => "assertion.organization_id",
          "attribute.project_id" => "assertion.project_id"
        },
        oidc: Google::Apis::IamV1::Oidc.new(
          issuer_uri: issuer,
          allowed_audiences: [workload_identity.audience]
        )
      )

      operation = iam.create_project_location_workload_identity_pool_provider(
        workload_identity_pool_resource_name,
        provider,
        workload_identity_pool_provider_id: workload_identity.workload_identity_provider
      )
      wait_for_operation!(iam, operation.name)
    end

    def bind_service_account(iam)
      policy = iam.get_project_service_account_iam_policy(service_account_resource_name)
      policy.version = 3
      policy.bindings ||= []

      member = "principalSet://iam.googleapis.com/#{workload_identity_pool_resource_name}/attribute.project_id/#{workload_identity.project_id}"
      binding = policy.bindings.find { |entry| entry.role == "roles/iam.workloadIdentityUser" }

      if binding
        binding.members ||= []
        binding.members << member unless binding.members.include?(member)
      else
        policy.bindings << Google::Apis::IamV1::Binding.new(role: "roles/iam.workloadIdentityUser", members: [member])
      end

      iam.set_service_account_iam_policy(
        service_account_resource_name,
        Google::Apis::IamV1::SetIamPolicyRequest.new(policy: policy)
      )
    end

    def service_account_name
      workload_identity.service_account_email.split("@").first
    end

    def service_account_resource_name
      "projects/#{workload_identity.gcp_project_id}/serviceAccounts/#{workload_identity.service_account_email}"
    end

    def workload_identity_pool_parent
      "projects/#{workload_identity.gcp_project_number}/locations/global"
    end

    def workload_identity_pool_resource_name
      "#{workload_identity_pool_parent}/workloadIdentityPools/#{workload_identity.workload_identity_pool}"
    end

    def workload_identity_provider_resource_name
      "#{workload_identity_pool_resource_name}/providers/#{workload_identity.workload_identity_provider}"
    end

    def project_label
      workload_identity.project&.name.presence || "project-#{workload_identity.project_id}"
    end

    def iam_service
      return @iam if defined?(@iam) && @iam

      iam = Google::Apis::IamV1::IamService.new
      iam.authorization = Gcp::Credentials.new(scope: SCOPE)
      iam
    end

    def wait_for_operation!(iam, name)
      OPERATION_RETRIES.times do
        operation = iam.get_project_location_workload_identity_pool_operation(name)
        return if operation.done && operation.error.blank?

        if operation.done && operation.error.present?
          raise "operation failed: #{operation.error.message}"
        end

        sleep OPERATION_SLEEP_SECONDS
      end

      raise "operation timed out: #{name}"
    end

    def not_found?(error)
      error.status_code.to_i == 404
    end
  end
end
