# frozen_string_literal: true

require "digest"
require "securerandom"

class NodeBootstrapToken < ApplicationRecord
  TOKEN_BYTES = 32
  TTL = 1.hour
  PURPOSE_MANUAL = "manual"
  PURPOSE_MANAGED_POOL_NODE = "managed_pool_node"
  PURPOSES = [
    PURPOSE_MANUAL,
    PURPOSE_MANAGED_POOL_NODE
  ].freeze

  belongs_to :organization, optional: true
  belongs_to :environment, optional: true
  belongs_to :issued_by_user, class_name: "User", optional: true
  belongs_to :node, optional: true

  validates :token_digest, presence: true
  validates :expires_at, presence: true
  validates :purpose, inclusion: { in: PURPOSES }
  validate :environment_belongs_to_organization

  def self.issue!(organization: nil, environment: nil, issued_by_user: nil, purpose: PURPOSE_MANUAL, managed_provider: nil, managed_region: nil, managed_size_slug: nil)
    raw_token = SecureRandom.hex(TOKEN_BYTES)
    record = create!(
      organization: organization,
      environment: environment,
      issued_by_user: issued_by_user,
      purpose: purpose,
      managed_provider: managed_provider,
      managed_region: managed_region,
      managed_size_slug: managed_size_slug,
      token_digest: digest(raw_token),
      expires_at: TTL.from_now
    )
    [record, raw_token]
  end

  def self.find_by_token(raw_token)
    return nil if raw_token.blank?

    find_by(token_digest: digest(raw_token))
  end

  def active?
    consumed_at.nil? && expires_at > Time.current
  end

  def consume!
    update!(consumed_at: Time.current)
  end

  def self.revoke_active_for(organization)
    where(organization: organization, purpose: PURPOSE_MANUAL, consumed_at: nil).update_all(consumed_at: Time.current)
  end

  def self.active_for(organization)
    where(organization: organization, purpose: PURPOSE_MANUAL, consumed_at: nil).where("expires_at > ?", Time.current).order(expires_at: :desc).first
  end

  def managed_pool_node?
    purpose == PURPOSE_MANAGED_POOL_NODE
  end

  def self.digest(value)
    Digest::SHA256.hexdigest(value)
  end

  private

  def environment_belongs_to_organization
    return if environment.blank?
    return errors.add(:organization, "must be present when environment is set") if organization.blank?
    return if environment.project&.organization_id == organization_id

    errors.add(:environment, "must belong to the bootstrap token organization")
  end
end
