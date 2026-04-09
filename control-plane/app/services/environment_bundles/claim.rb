# frozen_string_literal: true

module EnvironmentBundles
  class Claim
    Error = Class.new(StandardError)

    def initialize(environment:, broker: nil, provisioner_class: Provisioner)
      @environment = environment
      @broker = broker || Runtime::Broker.current
      @provisioner_class = provisioner_class
    end

    def call
      if environment.environment_bundle
        sync_environment!(environment.environment_bundle)
        return environment.environment_bundle
      end

      organization_bundle = environment.project.organization.organization_bundle
      raise Error, "organization bundle is required" unless organization_bundle

      bundle = reserve_bundle(organization_bundle)
      sync_environment!(bundle)
      Runtime::EnsureBundles.enqueue
      bundle
    rescue StandardError => error
      raise error if error.is_a?(Error)

      raise Error, error.message
    end

    private

    attr_reader :environment, :broker, :provisioner_class

    def reserve_bundle(organization_bundle)
      bundle = if use_warm_bundle_pool?
        warm_bundle(organization_bundle) || provisioner_class.new(organization_bundle:, broker:).call
      else
        provisioner_class.new(organization_bundle:, broker:).call
      end
      bundle.with_lock do
        raise Error, "environment bundle is no longer available" unless bundle.status == EnvironmentBundle::STATUS_WARM

        bundle.update!(
          claimed_by_environment: environment,
          claimed_at: Time.current,
          status: EnvironmentBundle::STATUS_CLAIMED
        )
      end
      bundle
    end

    def warm_bundle(organization_bundle)
      organization_bundle.environment_bundles.where(status: EnvironmentBundle::STATUS_WARM).order(:created_at).first
    end

    def use_warm_bundle_pool?
      environment.project.environments.where.not(id: environment.id).none?
    end

    def sync_environment!(bundle)
      environment.transaction do
        environment.update!(
          environment_bundle: bundle,
          runtime_project: bundle.runtime_project,
          gcp_project_id: bundle.runtime_project.gcp_project_id,
          gcp_project_number: bundle.runtime_project.gcp_project_number,
          workload_identity_pool: bundle.runtime_project.workload_identity_pool,
          workload_identity_provider: bundle.runtime_project.workload_identity_provider,
          service_account_email: bundle.service_account_email
        )

        ingress = environment.environment_ingress || environment.build_environment_ingress
        ingress.hostname = bundle.hostname
        ingress.cloudflare_tunnel_id = bundle.cloudflare_tunnel_id
        ingress.gcp_secret_name = bundle.gcp_secret_name
        ingress.status = if environment.direct_dns_ingress?
          EnvironmentIngress::STATUS_PENDING
        else
          EnvironmentIngress::STATUS_READY
        end
        ingress.last_error = nil
        ingress.provisioned_at = bundle.provisioned_at || Time.current
        ingress.save!
      end
    end
  end
end
