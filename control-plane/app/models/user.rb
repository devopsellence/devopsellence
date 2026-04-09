# frozen_string_literal: true

require "digest"
require "uri"

class User < ApplicationRecord
  ACCOUNT_KIND_HUMAN = "human"
  ACCOUNT_KIND_ANONYMOUS = "anonymous"
  ACCOUNT_KINDS = [
    ACCOUNT_KIND_HUMAN,
    ACCOUNT_KIND_ANONYMOUS
  ].freeze

  AnonymousAuthenticationError = Class.new(StandardError)

  before_validation :normalize_email
  before_validation :normalize_anonymous_identifier
  before_validation :infer_account_kind

  has_many :user_identities, dependent: :destroy
  has_many :organization_memberships, dependent: :destroy
  has_many :organizations, through: :organization_memberships
  has_many :api_tokens, dependent: :destroy
  has_many :owned_organization_memberships, -> { where(role: OrganizationMembership::ROLE_OWNER) },
    class_name: "OrganizationMembership",
    inverse_of: :user
  has_many :owned_organizations, through: :owned_organization_memberships, source: :organization
  has_many :created_organization_workload_identities,
    class_name: "OrganizationWorkloadIdentity",
    foreign_key: :created_by_user_id,
    inverse_of: :created_by_user,
    dependent: :nullify
  has_many :claim_links, dependent: :destroy

  validates :account_kind, inclusion: { in: ACCOUNT_KINDS }
  validates :email, presence: true, format: { with: URI::MailTo::EMAIL_REGEXP }, if: :human?
  validates :email, uniqueness: { case_sensitive: false }, allow_nil: true
  validates :anonymous_identifier, presence: true, uniqueness: true, if: :anonymous?
  validates :anonymous_secret_digest, presence: true, if: :anonymous?
  validates :email, absence: true, if: :anonymous?

  class << self
    def anonymous_secret_digest(raw_secret)
      Digest::SHA256.hexdigest(raw_secret.to_s)
    end

    def bootstrap_anonymous!(identifier:, raw_secret:)
      normalized_identifier = identifier.to_s.strip
      raise AnonymousAuthenticationError, "missing anonymous_id" if normalized_identifier.blank?
      raise AnonymousAuthenticationError, "missing anonymous_secret" if raw_secret.blank?

      user = find_or_initialize_by(anonymous_identifier: normalized_identifier)
      if user.new_record?
        user.account_kind = ACCOUNT_KIND_ANONYMOUS
        user.anonymous_identifier = normalized_identifier
        user.anonymous_secret_digest = anonymous_secret_digest(raw_secret)
        user.save!
        return user
      end

      raise AnonymousAuthenticationError, "anonymous account has already been claimed" unless user.anonymous?
      raise AnonymousAuthenticationError, "invalid anonymous_secret" unless user.anonymous_secret_matches?(raw_secret)

      user
    end
  end

  def human?
    account_kind == ACCOUNT_KIND_HUMAN
  end

  def anonymous?
    account_kind == ACCOUNT_KIND_ANONYMOUS
  end

  def confirm!
    update!(confirmed_at: Time.current)
  end

  def anonymous_secret_matches?(raw_secret)
    return false if anonymous_secret_digest.blank? || raw_secret.blank?

    ActiveSupport::SecurityUtils.secure_compare(
      anonymous_secret_digest,
      self.class.anonymous_secret_digest(raw_secret)
    )
  end

  def claim!(email:)
    update!(
      account_kind: ACCOUNT_KIND_HUMAN,
      email: email,
      confirmed_at: confirmed_at || Time.current,
      claimed_at: Time.current,
      anonymous_identifier: nil,
      anonymous_secret_digest: nil
    )
  end

  private

  def normalize_email
    normalized = email.to_s.strip.downcase
    self.email = normalized.presence
  end

  def normalize_anonymous_identifier
    normalized = anonymous_identifier.to_s.strip
    self.anonymous_identifier = normalized.presence
  end

  def infer_account_kind
    self.account_kind = if account_kind.present?
      account_kind
    elsif email.present?
      ACCOUNT_KIND_HUMAN
    elsif anonymous_identifier.present? || anonymous_secret_digest.present?
      ACCOUNT_KIND_ANONYMOUS
    else
      ACCOUNT_KIND_HUMAN
    end
  end
end
