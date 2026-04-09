# frozen_string_literal: true

module OrganizationBundles
  class Claim
    Error = Class.new(StandardError)

    def initialize(organization:, broker: nil, provisioner_class: Provisioner)
      @organization = organization
      @broker = broker || Runtime::Broker.current
      @provisioner_class = provisioner_class
    end

    def call
      if organization.organization_bundle
        sync_organization!(organization.organization_bundle)
        return organization.organization_bundle
      end

      bundle = reserve_bundle
      sync_organization!(bundle)
      Runtime::EnsureBundles.enqueue
      bundle
    rescue StandardError => error
      raise error if error.is_a?(Error)

      raise Error, error.message
    end

    private

    attr_reader :organization, :broker, :provisioner_class

    def reserve_bundle
      bundle = warm_bundle || provisioner_class.new(runtime_project: organization.active_runtime_project, broker:).call
      bundle.with_lock do
        raise Error, "organization bundle is no longer available" unless bundle.status == OrganizationBundle::STATUS_WARM

        bundle.update!(
          claimed_by_organization: organization,
          claimed_at: Time.current,
          status: OrganizationBundle::STATUS_CLAIMED
        )
      end
      bundle
    end

    def warm_bundle
      OrganizationBundle
        .where(runtime_project: organization.active_runtime_project, status: OrganizationBundle::STATUS_WARM)
        .order(:created_at)
        .first
    end

    def sync_organization!(bundle)
      runtime_project = bundle.runtime_project
      organization.update!(
        organization_bundle: bundle,
        runtime_project: runtime_project,
        gcp_project_id: runtime_project.gcp_project_id,
        gcp_project_number: runtime_project.gcp_project_number,
        workload_identity_pool: runtime_project.workload_identity_pool,
        workload_identity_provider: runtime_project.workload_identity_provider,
        gar_repository_region: bundle.gar_repository_region,
        gcs_bucket_name: bundle.gcs_bucket_name,
        gar_repository_name: bundle.gar_repository_name,
        provisioning_status: Organization::PROVISIONING_READY,
        provisioning_error: nil
      )
    end
  end
end
