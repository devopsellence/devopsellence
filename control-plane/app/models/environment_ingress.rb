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
  before_validation :normalize_hostname!
  after_commit :ensure_primary_host_record!, on: [ :create, :update ]

  validates :hostname, presence: true, uniqueness: true
  validates :gcp_secret_name, presence: true, uniqueness: true
  validates :status, inclusion: { in: STATUSES }

  def hosts
    persisted = environment_ingress_hosts.map { |entry| normalize_host(entry.hostname) }.reject(&:blank?)
    return persisted if persisted.any?

    normalized_hostname = normalize_host(hostname)
    normalized_hostname.present? ? [ normalized_hostname ] : []
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
    hosts.include?(normalize_host(candidate))
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
      Array(values).map { |entry| normalize_host(entry) }.reject(&:blank?).uniq
    end

    def normalize_host(value)
      IngressHostnames.normalize(value)
    end

    def ensure_primary_host_record!
      normalized_hostname = normalize_host(hostname)
      return if normalized_hostname.blank?
      return if environment_ingress_hosts.exists?(hostname: normalized_hostname)

      assign_hosts!([ normalized_hostname ])
    end

    def normalize_hostname!
      self.hostname = normalize_host(hostname)
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
