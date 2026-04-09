# frozen_string_literal: true

class OrganizationWorkloadIdentity < ApplicationRecord
  STATUS_READY = "ready"
  STATUS_FAILED = "failed"
  STATUSES = [STATUS_READY, STATUS_FAILED].freeze

  belongs_to :organization
  belongs_to :project
  belongs_to :created_by_user, class_name: "User", optional: true

  before_validation :normalize_status

  validates :gcp_project_id, presence: true
  validates :gcp_project_number, presence: true
  validates :service_account_email, presence: true
  validates :workload_identity_pool, presence: true
  validates :workload_identity_provider, presence: true
  validates :status, presence: true, inclusion: { in: STATUSES }
  validates :project_id, uniqueness: { scope: :organization_id }

  scope :ready, -> { where(status: STATUS_READY) }

  def audience
    pool = workload_identity_pool.to_s.strip
    provider = workload_identity_provider.to_s.strip.sub(%r{\A//iam\.googleapis\.com/}, "")

    unless pool.match?(%r{\Aprojects/\d+/locations/global/workloadIdentityPools/[a-z0-9-]+\z})
      raise ArgumentError, "organization_workload_identity.workload_identity_pool must be a full workload identity pool resource name"
    end

    unless provider.match?(%r{\A#{Regexp.escape(pool)}/providers/[a-z0-9-]+\z})
      raise ArgumentError, "organization_workload_identity.workload_identity_provider must be a full workload identity provider resource name"
    end

    "//iam.googleapis.com/#{provider}"
  end

  private

  def normalize_status
    self.status = STATUS_FAILED if status.blank? || status == "pending_manual"
  end
end
