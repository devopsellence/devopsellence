# frozen_string_literal: true

require "digest"

class EnvironmentSecret < ApplicationRecord
  ACCESS_VERIFY_INTERVAL = 1.day
  VARIABLE_NAME_FORMAT = /\A[A-Za-z_][A-Za-z0-9_]*\z/
  SERVICE_NAME_FORMAT = /\A[a-z][a-z0-9-]*\z/

  belongs_to :environment

  encrypts :value

  validates :service_name, presence: true
  validates :service_name, format: { with: SERVICE_NAME_FORMAT }
  validates :name, presence: true, format: { with: VARIABLE_NAME_FORMAT }
  validates :gcp_secret_name, presence: true, uniqueness: true
  validates :name, uniqueness: { scope: [ :environment_id, :service_name ] }

  before_validation :normalize_service_name
  before_validation :assign_gcp_secret_name

  def secret_ref
    if environment.active_runtime_project.standalone?
      base_url = PublicBaseUrl.configured
      raise "standalone secret ref requires DEVOPSELLENCE_PUBLIC_BASE_URL" if base_url.blank?

      return "#{base_url}/api/v1/agent/secrets/environment_secrets/#{id}"
    end

    "gsm://projects/#{environment.gcp_project_id}/secrets/#{gcp_secret_name}/versions/latest"
  end

  def self.value_sha256(value)
    Digest::SHA256.hexdigest(value.to_s)
  end

  def self.build_gcp_secret_name(environment:, service_name:, name:)
    env_slug = environment.environment_bundle&.token || SecureRandom.alphanumeric(12).downcase
    raw = [
      "env",
      env_slug,
      normalize_service_name_value(service_name),
      name.to_s.downcase.gsub(/[^a-z0-9]+/, "-").gsub(/\A-+|-+\z/, "")
    ].reject(&:blank?).join("-")
    raw[0, 255]
  end

  def self.normalize_service_name_value(value)
    value.to_s.strip.downcase.gsub(/[^a-z0-9]+/, "-").gsub(/\A-+|-+\z/, "")
  end

  def access_verified_for?(service_account_email, time: Time.current)
    grantee = service_account_email.to_s.strip
    return false if grantee.blank?
    return false if access_grantee_email != grantee
    return false if access_verified_at.blank?

    access_verified_at > time - ACCESS_VERIFY_INTERVAL
  end

  private

  def normalize_service_name
    self.service_name = self.class.normalize_service_name_value(service_name)
  end

  def assign_gcp_secret_name
    return if gcp_secret_name.present? || environment.blank?

    self.gcp_secret_name = self.class.build_gcp_secret_name(
      environment: environment,
      service_name: service_name,
      name: name
    )
  end
end
