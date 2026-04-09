# frozen_string_literal: true

class EnvironmentIngress < ApplicationRecord
  HOSTNAME_LENGTH = 6
  STATUS_PENDING = "pending"
  STATUS_READY = "ready"
  STATUS_DEGRADED = "degraded"
  STATUS_FAILED = "failed"
  STATUSES = [
    STATUS_PENDING,
    STATUS_READY,
    STATUS_DEGRADED,
    STATUS_FAILED
  ].freeze

  belongs_to :environment

  before_validation :assign_gcp_secret_name

  validates :hostname, presence: true, uniqueness: true
  validates :gcp_secret_name, presence: true, uniqueness: true
  validates :status, inclusion: { in: STATUSES }

  def public_url
    Devopsellence::IngressConfig.public_url(hostname)
  end

  def tunnel_token_secret_ref
    return nil if gcp_secret_name.blank?
    if environment.active_runtime_project.standalone?
      base_url = PublicBaseUrl.configured
      raise "standalone ingress secret ref requires DEVOPSELLENCE_PUBLIC_BASE_URL" if base_url.blank?

      bundle = environment.environment_bundle
      return nil unless bundle

      return "#{base_url}/api/v1/agent/secrets/environment_bundles/#{bundle.id}/tunnel_token"
    end

    "gsm://projects/#{environment.gcp_project_id}/secrets/#{gcp_secret_name}/versions/latest"
  end

  def ready?
    status == STATUS_READY
  end

  def degraded?
    status == STATUS_DEGRADED
  end

  private
    def assign_gcp_secret_name
      return if gcp_secret_name.present? || environment.blank?

      if environment.environment_bundle&.gcp_secret_name.present?
        self.gcp_secret_name = environment.environment_bundle.gcp_secret_name
        return
      end
      env_slug = environment.environment_bundle&.token || SecureRandom.alphanumeric(12).downcase
      self.gcp_secret_name = "env-#{env_slug}-ingress-cloudflare-tunnel-token"
    end
end
