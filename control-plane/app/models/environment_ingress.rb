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
  has_many :environment_ingress_hosts, -> { order(:position, :id) }, dependent: :destroy

  before_validation :assign_gcp_secret_name
  after_commit :ensure_primary_host_record!, on: [ :create, :update ]

  validates :hostname, presence: true, uniqueness: true
  validates :gcp_secret_name, presence: true, uniqueness: true
  validates :status, inclusion: { in: STATUSES }

  def hosts
    persisted = environment_ingress_hosts.map { |entry| entry.hostname.to_s.strip }.reject(&:blank?)
    return persisted if persisted.any?

    hostname.to_s.strip.present? ? [ hostname.to_s.strip ] : []
  end

  def public_urls
    hosts.map { |host| Devopsellence::IngressConfig.public_url(host) }.compact
  end

  def primary_hostname
    hosts.first
  end

  def public_url
    Devopsellence::IngressConfig.public_url(primary_hostname)
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

  def hostname_matches?(candidate)
    hosts.include?(candidate.to_s.strip)
  end

  def assign_hosts!(values)
    normalized = normalize_hosts(values)
    raise ArgumentError, "hosts must include at least one host" if normalized.empty?

    transaction do
      update!(hostname: normalized.first)
      environment_ingress_hosts.destroy_all
      normalized.each_with_index do |entry, index|
        environment_ingress_hosts.create!(hostname: entry, position: index)
      end
    end
    reload
  end

  private
    def normalize_hosts(values)
      Array(values).map { |entry| entry.to_s.strip }.reject(&:blank?).uniq
    end

    def ensure_primary_host_record!
      return unless hostname.to_s.strip.present?
      return if environment_ingress_hosts.exists?(hostname: hostname)

      assign_hosts!([ hostname ])
    end

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
