# frozen_string_literal: true

require "digest"
require "securerandom"

class ApiToken < ApplicationRecord
  ACCESS_TTL = 1.hour
  REFRESH_TTL = 30.days
  CI_TOKEN_TTL = 100.years
  TOKEN_BYTES = 32
  LAST_USED_AT_TOUCH_INTERVAL = 1.minute

  belongs_to :user

  validates :access_token_digest, presence: true
  validates :refresh_token_digest, presence: true
  validates :access_expires_at, presence: true
  validates :refresh_expires_at, presence: true

  def self.issue!(user:)
    raw_access = SecureRandom.hex(TOKEN_BYTES)
    raw_refresh = SecureRandom.hex(TOKEN_BYTES)

    token = create!(
      user: user,
      access_token_digest: digest(raw_access),
      refresh_token_digest: digest(raw_refresh),
      access_expires_at: ACCESS_TTL.from_now,
      refresh_expires_at: REFRESH_TTL.from_now
    )

    [token, raw_access, raw_refresh]
  end

  def self.digest(value)
    Digest::SHA256.hexdigest(value)
  end

  def self.issue_ci_token!(user:, name:)
    raw_access = SecureRandom.hex(TOKEN_BYTES)
    raw_refresh = SecureRandom.hex(TOKEN_BYTES)

    token = create!(
      user: user,
      name: name,
      access_token_digest: digest(raw_access),
      refresh_token_digest: digest(raw_refresh),
      access_expires_at: CI_TOKEN_TTL.from_now,
      refresh_expires_at: CI_TOKEN_TTL.from_now
    )

    [token, raw_access]
  end

  def self.find_by_refresh_token(raw_refresh)
    return nil if raw_refresh.blank?

    find_by(refresh_token_digest: digest(raw_refresh))
  end

  def self.find_by_access_token(raw_access)
    return nil if raw_access.blank?

    find_by(access_token_digest: digest(raw_access))
  end

  def refresh_active?
    revoked_at.nil? && refresh_expires_at > Time.current
  end

  def access_active?
    revoked_at.nil? && access_expires_at > Time.current
  end

  def rotate!
    raw_access = SecureRandom.hex(TOKEN_BYTES)
    raw_refresh = SecureRandom.hex(TOKEN_BYTES)

    update!(
      access_token_digest: self.class.digest(raw_access),
      refresh_token_digest: self.class.digest(raw_refresh),
      access_expires_at: ACCESS_TTL.from_now,
      refresh_expires_at: REFRESH_TTL.from_now,
      last_used_at: Time.current
    )

    [raw_access, raw_refresh]
  end

  def revoke!(revoked_at: Time.current)
    update!(
      revoked_at: revoked_at,
      access_expires_at: [ access_expires_at, revoked_at ].compact.min,
      refresh_expires_at: [ refresh_expires_at, revoked_at ].compact.min
    )
  end

  def touch_last_used_at_if_stale!(time: Time.current)
    stale = last_used_at.blank? || last_used_at < (time - LAST_USED_AT_TOUCH_INTERVAL)
    update_column(:last_used_at, time) if stale
  end
end
