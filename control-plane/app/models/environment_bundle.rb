# frozen_string_literal: true

require "securerandom"

class EnvironmentBundle < ApplicationRecord
  STATUS_PROVISIONING = "provisioning"
  STATUS_WARM = "warm"
  STATUS_CLAIMED = "claimed"
  STATUS_FAILED = "failed"
  STATUSES = [ STATUS_PROVISIONING, STATUS_WARM, STATUS_CLAIMED, STATUS_FAILED ].freeze
  TOKEN_LENGTH = 12

  belongs_to :runtime_project
  belongs_to :organization_bundle
  belongs_to :claimed_by_environment, class_name: "Environment", optional: true
  has_many :node_bundles, dependent: :destroy

  encrypts :tunnel_token

  validates :token, presence: true, uniqueness: true
  validates :service_account_email, presence: true, uniqueness: true
  validates :gcp_secret_name, presence: true, uniqueness: true
  validates :status, inclusion: { in: STATUSES }

  scope :warm, -> { where(status: STATUS_WARM) }

  before_validation :assign_defaults

  def self.generate_token
    "a#{SecureRandom.alphanumeric(TOKEN_LENGTH - 1).downcase}"
  end

  delegate :gcp_project_id, :gcp_project_number, :audience, to: :runtime_project

  def identity_version
    claimed_by_environment&.identity_version.to_i
  end

  def project_id
    claimed_by_environment&.project_id
  end

  private

  def assign_defaults
    self.token = self.class.generate_token if token.blank?
    return if runtime_project.blank? || token.blank?

    if runtime_project.standalone?
      self.service_account_email = "standalone-eb-#{token}" if service_account_email.blank?
      self.gcp_secret_name = "eb-#{token}-ingress-cloudflare-tunnel-token" if gcp_secret_name.blank?
      return
    end

    if service_account_email.blank?
      account_id = "eb#{token}"[0, 30]
      self.service_account_email = "#{account_id}@#{runtime_project.gcp_project_id}.iam.gserviceaccount.com"
    end
    self.gcp_secret_name = "eb-#{token}-ingress-cloudflare-tunnel-token" if gcp_secret_name.blank?
  end
end
