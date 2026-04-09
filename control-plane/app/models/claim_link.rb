# frozen_string_literal: true

require "digest"
require "securerandom"
require "uri"

class ClaimLink < ApplicationRecord
  TOKEN_BYTES = 32
  TTL = 15.minutes

  belongs_to :user

  validates :email, presence: true, format: { with: URI::MailTo::EMAIL_REGEXP }
  validates :token_digest, presence: true
  validates :expires_at, presence: true

  class << self
    def issue!(user:, email:, request:)
      raw_token = SecureRandom.hex(TOKEN_BYTES)
      record = create!(
        user: user,
        email: email.to_s.strip.downcase,
        token_digest: digest(raw_token),
        expires_at: TTL.from_now,
        ip_address: request.remote_ip,
        user_agent: request.user_agent
      )
      [record, raw_token]
    end

    def find_valid(raw_token)
      return nil if raw_token.blank?

      record = find_by(token_digest: digest(raw_token))
      return nil unless record&.active?

      record
    end

    def digest(value)
      Digest::SHA256.hexdigest(value)
    end
  end

  def active?
    consumed_at.blank? && expires_at > Time.current
  end

  def consume!
    update!(consumed_at: Time.current)
  end
end
