# frozen_string_literal: true

require "securerandom"

class NodeBundle < ApplicationRecord
  STATUS_PROVISIONING = "provisioning"
  STATUS_WARM = "warm"
  STATUS_CLAIMED = "claimed"
  STATUS_FAILED = "failed"
  STATUSES = [ STATUS_PROVISIONING, STATUS_WARM, STATUS_CLAIMED, STATUS_FAILED ].freeze
  TOKEN_LENGTH = 12

  belongs_to :runtime_project
  belongs_to :organization_bundle
  belongs_to :environment_bundle
  belongs_to :node, optional: true
  has_many :standalone_desired_state_documents, dependent: :destroy

  validates :token, presence: true, uniqueness: true
  validates :status, inclusion: { in: STATUSES }
  validates :desired_state_object_path, presence: true
  validates :desired_state_sequence, numericality: { greater_than_or_equal_to: 0 }

  scope :warm, -> { where(status: STATUS_WARM) }

  before_validation :assign_defaults

  def self.generate_token
    "a#{SecureRandom.alphanumeric(TOKEN_LENGTH - 1).downcase}"
  end

  def desired_state_bucket
    return "" if runtime_project&.standalone?

    organization_bundle&.gcs_bucket_name.to_s
  end

  def desired_state_uri
    if runtime_project&.standalone?
      base_url = PublicBaseUrl.configured
      return nil if base_url.blank?

      return "#{base_url}/api/v1/agent/desired_state"
    end

    return nil if desired_state_bucket.blank? || desired_state_object_path.blank?

    "gs://#{desired_state_bucket}/#{desired_state_object_path}"
  end

  private

  def assign_defaults
    self.token = self.class.generate_token if token.blank?
    self.desired_state_object_path = "node-bundles/#{token}/desired_state.json" if desired_state_object_path.blank? && token.present?
  end
end
