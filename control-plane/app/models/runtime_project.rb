# frozen_string_literal: true

class RuntimeProject < ApplicationRecord
  BACKEND_GCP = "gcp"
  BACKEND_STANDALONE = "standalone"
  RUNTIME_BACKENDS = [
    BACKEND_GCP,
    BACKEND_STANDALONE
  ].freeze
  KIND_SHARED_SANDBOX = "shared_sandbox"
  KIND_DEDICATED = "dedicated"
  KINDS = [
    KIND_SHARED_SANDBOX,
    KIND_DEDICATED
  ].freeze
  DEFAULT_SLUG = "shared-sandbox"

  has_many :organizations, dependent: :restrict_with_error
  has_many :environments, dependent: :restrict_with_error

  validates :name, presence: true
  validates :slug, presence: true, uniqueness: true
  validates :kind, presence: true, inclusion: { in: KINDS }
  validates :runtime_backend, presence: true, inclusion: { in: RUNTIME_BACKENDS }
  validates :gcp_project_id, presence: true, if: :gcp?
  validates :gcp_project_number, presence: true, if: :gcp?
  validates :workload_identity_pool, presence: true, if: :gcp?
  validates :workload_identity_provider, presence: true, if: :gcp?
  validates :gar_region, presence: true, if: :gcp?
  validates :gcs_bucket_prefix, presence: true, if: :gcp?

  class << self
    def default!
      runtime = Devopsellence::RuntimeConfig.current
      project = find_or_initialize_by(slug: DEFAULT_SLUG)
      project.assign_attributes(
        name: "Shared Sandbox Runtime",
        kind: KIND_SHARED_SANDBOX,
        runtime_backend: runtime.runtime_backend,
        gcp_project_id: runtime.gcp_project_id,
        gcp_project_number: runtime.gcp_project_number,
        workload_identity_pool: runtime.workload_identity_pool,
        workload_identity_provider: runtime.workload_identity_provider,
        gar_region: runtime.gar_region,
        gcs_bucket_prefix: runtime.gcs_bucket_prefix
      )
      project.save! if project.new_record? || project.changed?
      project
    end
  end

  def audience
    return nil unless gcp?

    "//iam.googleapis.com/#{workload_identity_provider_resource_name}"
  end

  def gcp?
    runtime_backend == BACKEND_GCP
  end

  def standalone?
    runtime_backend == BACKEND_STANDALONE
  end

  def workload_identity_pool_resource_name
    raise ArgumentError, "runtime_project.workload_identity_pool is unavailable for standalone backend" unless gcp?

    value = workload_identity_pool.to_s.strip
    return value if value.match?(%r{\Aprojects/\d+/locations/global/workloadIdentityPools/[a-z0-9-]+\z})

    raise ArgumentError, "runtime_project.workload_identity_pool must be a full workload identity pool resource name"
  end

  def workload_identity_provider_resource_name
    raise ArgumentError, "runtime_project.workload_identity_provider is unavailable for standalone backend" unless gcp?

    value = workload_identity_provider.to_s.strip.sub(%r{\A//iam\.googleapis\.com/}, "")
    return value if value.match?(%r{\A#{Regexp.escape(workload_identity_pool_resource_name)}/providers/[a-z0-9-]+\z})

    raise ArgumentError, "runtime_project.workload_identity_provider must be a full workload identity provider resource name"
  end
end
