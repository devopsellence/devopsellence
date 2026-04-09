# frozen_string_literal: true

require "securerandom"

class OrganizationBundle < ApplicationRecord
  STATUS_PROVISIONING = "provisioning"
  STATUS_WARM = "warm"
  STATUS_CLAIMED = "claimed"
  STATUS_FAILED = "failed"
  STATUSES = [ STATUS_PROVISIONING, STATUS_WARM, STATUS_CLAIMED, STATUS_FAILED ].freeze
  TOKEN_LENGTH = 12

  belongs_to :runtime_project
  belongs_to :claimed_by_organization, class_name: "Organization", optional: true
  has_many :environment_bundles, dependent: :destroy

  validates :token, presence: true, uniqueness: true
  validates :gcs_bucket_name, presence: true, uniqueness: true
  validates :gar_repository_name, presence: true, uniqueness: true
  validates :gar_repository_region, presence: true
  validates :gar_writer_service_account_email, presence: true, uniqueness: true
  validates :status, inclusion: { in: STATUSES }

  scope :warm, -> { where(status: STATUS_WARM) }

  before_validation :assign_defaults

  def self.generate_token
    "a#{SecureRandom.alphanumeric(TOKEN_LENGTH - 1).downcase}"
  end

  def gar_repository_path
    return claimed_by_organization.organization_registry_config.repository_path if runtime_project&.standalone? && claimed_by_organization&.organization_registry_config
    if Devopsellence::RuntimeConfig.current.gar_host_override.present?
      return "#{Devopsellence::RuntimeConfig.current.gar_host_override}/#{runtime_project.gcp_project_id}/#{gar_repository_name}"
    end

    "#{gar_repository_region}-docker.pkg.dev/#{runtime_project.gcp_project_id}/#{gar_repository_name}"
  end

  private

  def assign_defaults
    self.token = self.class.generate_token if token.blank?
    return if runtime_project.blank?

    if runtime_project.standalone?
      self.gar_repository_region = "standalone" if gar_repository_region.blank?
      self.gcs_bucket_name = "standalone-ob-#{token}" if gcs_bucket_name.blank? && token.present?
      self.gar_repository_name = "standalone-ob-#{token}-apps" if gar_repository_name.blank? && token.present?
      self.gar_writer_service_account_email = "standalone-ob-#{token}-writer" if gar_writer_service_account_email.blank? && token.present?
      return
    end

    self.gar_repository_region = runtime_project.gar_region if gar_repository_region.blank?
    self.gcs_bucket_name = "#{runtime_project.gcs_bucket_prefix}-ob-#{token}" if gcs_bucket_name.blank? && token.present?
    self.gar_repository_name = "ob-#{token}-apps" if gar_repository_name.blank? && token.present?
    if gar_writer_service_account_email.blank? && token.present?
      account_id = "ob#{token}garpush"[0, 30]
      self.gar_writer_service_account_email = "#{account_id}@#{runtime_project.gcp_project_id}.iam.gserviceaccount.com"
    end
  end
end
