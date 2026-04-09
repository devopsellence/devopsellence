# frozen_string_literal: true

class UserIdentity < ApplicationRecord
  PROVIDERS = %w[google github].freeze

  belongs_to :user

  validates :provider, presence: true, inclusion: { in: PROVIDERS }, uniqueness: { scope: :user_id }
  validates :provider_uid, presence: true, uniqueness: { scope: :provider }
  validates :email, presence: true

  before_validation :normalize_fields

  def profile
    JSON.parse(profile_json.presence || "{}")
  rescue JSON::ParserError
    {}
  end

  def profile=(value)
    self.profile_json = JSON.dump(value || {})
  end

  private

  def normalize_fields
    self.provider = provider.to_s.strip.downcase
    self.provider_uid = provider_uid.to_s.strip
    self.email = email.to_s.strip.downcase
  end
end
