# frozen_string_literal: true

require "digest"
require "json"
require "securerandom"

class Node < ApplicationRecord
  LAST_SEEN_TOUCH_INTERVAL = 1.minute
  LAST_SEEN_LOCK_TIMEOUT = "100ms"
  CAPABILITY_DIRECT_DNS_INGRESS = "direct_dns_ingress.v1"
  CAPABILITY_RELEASE_COMMAND = "release_command.v1"
  LABEL_WEB = "web"
  LABEL_WORKER = "worker"
  LABELS = [LABEL_WEB, LABEL_WORKER].freeze
  INGRESS_TLS_PENDING = "pending"
  INGRESS_TLS_READY = "ready"
  INGRESS_TLS_FAILED = "failed"
  INGRESS_TLS_STATUSES = [
    "",
    INGRESS_TLS_PENDING,
    INGRESS_TLS_READY,
    INGRESS_TLS_FAILED
  ].freeze
  TOKEN_BYTES = 32
  ACCESS_TTL = 1.hour
  REFRESH_TTL = 30.days
  PROVISIONING_READY = "ready"
  PROVISIONING_BOOTSTRAPPING = "bootstrapping"
  PROVISIONING_DELETING = "deleting"
  PROVISIONING_FAILED = "failed"
  PROVISIONING_STATUSES = [
    PROVISIONING_BOOTSTRAPPING,
    PROVISIONING_DELETING,
    PROVISIONING_READY,
    PROVISIONING_FAILED
  ].freeze

  belongs_to :organization, optional: true
  belongs_to :environment, optional: true
  belongs_to :node_bundle, optional: true
  has_many :deployment_node_statuses, dependent: :destroy
  has_many :node_diagnose_requests, dependent: :destroy
  has_many :node_bootstrap_tokens, dependent: :nullify
  has_many :standalone_desired_state_documents, dependent: :destroy

  before_validation :normalize_provisioning_status
  before_validation :normalize_capabilities_json

  ProvisioningError = Class.new(StandardError)

  validates :access_token_digest, presence: true
  validates :refresh_token_digest, presence: true
  validates :access_expires_at, presence: true
  validates :refresh_expires_at, presence: true
  validates :provisioning_status, inclusion: { in: PROVISIONING_STATUSES }
  validates :ingress_tls_status, inclusion: { in: INGRESS_TLS_STATUSES }
  validates :desired_state_sequence, numericality: { greater_than_or_equal_to: 0 }
  validates :managed_provider, presence: true, if: :managed?
  validates :managed_region, presence: true, if: :managed?
  validates :managed_size_slug, presence: true, if: :managed?
  validates :provider_server_id, presence: true, if: :managed?
  validate :labels_json_is_array

  def self.issue!(organization:, environment: nil, name: nil)
    raw_access = SecureRandom.hex(TOKEN_BYTES)
    raw_refresh = SecureRandom.hex(TOKEN_BYTES)
    record = create!(
      organization: organization,
      environment: environment,
      name: name,
      access_token_digest: digest(raw_access),
      refresh_token_digest: digest(raw_refresh),
      access_expires_at: ACCESS_TTL.from_now,
      refresh_expires_at: REFRESH_TTL.from_now,
      provisioning_status: PROVISIONING_READY
    )

    [record, raw_access, raw_refresh]
  end

  def self.find_by_access_token(raw_access)
    return nil if raw_access.blank?

    find_by(access_token_digest: digest(raw_access))
  end

  def self.find_by_refresh_token(raw_refresh)
    return nil if raw_refresh.blank?

    find_by(refresh_token_digest: digest(raw_refresh))
  end

  def access_active?
    revoked_at.nil? && access_expires_at.present? && access_expires_at > Time.current
  end

  def refresh_active?
    revoked_at.nil? && refresh_expires_at.present? && refresh_expires_at > Time.current
  end

  def rotate_tokens!
    raw_access = SecureRandom.hex(TOKEN_BYTES)
    raw_refresh = SecureRandom.hex(TOKEN_BYTES)

    update!(
      access_token_digest: self.class.digest(raw_access),
      refresh_token_digest: self.class.digest(raw_refresh),
      access_expires_at: ACCESS_TTL.from_now,
      refresh_expires_at: REFRESH_TTL.from_now,
      last_seen_at: Time.current
    )

    [raw_access, raw_refresh]
  end

  def self.digest(value)
    Digest::SHA256.hexdigest(value)
  end

  def desired_state_uri
    if desired_state_runtime_project&.standalone?
      base_url = PublicBaseUrl.configured
      return nil if base_url.blank?

      return "#{base_url}/api/v1/agent/desired_state"
    end

    return nil if desired_state_bucket.blank? || desired_state_object_path.blank?

    "gs://#{desired_state_bucket}/#{desired_state_object_path}"
  end

  def assigned?
    environment_id.present?
  end

  def assignment_ready?
    node_bundle_id.present? && desired_state_sequence.to_i > 0 && desired_state_uri.present?
  end

  def warm_pool_candidate?
    managed? &&
      !assigned? &&
      organization_id.nil? &&
      node_bundle_id.nil? &&
      lease_expires_at.nil? &&
      revoked_at.nil? &&
      provisioning_status == PROVISIONING_READY
  end

  def labels
    parsed = JSON.parse(labels_json.presence || '["web"]')
    return [LABEL_WEB] unless parsed.is_a?(Array)

    parsed.filter_map do |entry|
      label = entry.to_s.strip
      label.presence
    end
  rescue JSON::ParserError
    [LABEL_WEB]
  end

  def capabilities
    parsed = JSON.parse(capabilities_json.presence || "[]")
    return [] unless parsed.is_a?(Array)

    parsed.filter_map do |entry|
      capability = entry.to_s.strip
      capability.presence
    end
  rescue JSON::ParserError
    []
  end

  def capabilities=(value)
    normalized = Array(value).filter_map do |entry|
      capability = entry.to_s.strip
      capability.presence
    end.uniq.sort
    self.capabilities_json = JSON.generate(normalized)
  end

  def supports_capability?(capability)
    capabilities.include?(capability.to_s)
  end

  def ingress_tls_ready?
    ingress_tls_status == INGRESS_TLS_READY
  end

  def labels=(value)
    normalized = Array(value).filter_map do |entry|
      label = entry.to_s.strip
      label.presence
    end.uniq
    normalized = [LABEL_WEB] if normalized.empty?
    self.labels_json = JSON.generate(normalized)
  end

  def labeled?(label)
    labels.include?(label.to_s)
  end

  def touch_last_seen_at_if_stale!(time: Time.current)
    return false unless last_seen_stale_at?(time)

    with_last_seen_lock_timeout do
      updated = self.class.where(id: id)
        .where("last_seen_at IS NULL OR last_seen_at <= ?", time - LAST_SEEN_TOUCH_INTERVAL)
        .update_all(last_seen_at: time)
      if updated.positive?
        self.last_seen_at = time
        return true
      end
    end

    false
  rescue ActiveRecord::LockWaitTimeout, ActiveRecord::StatementInvalid => error
    raise unless last_seen_lock_timeout_error?(error)

    false
  end

  private
    def desired_state_runtime_project
      node_bundle&.runtime_project || environment&.runtime_project || organization&.runtime_project
    end

    def last_seen_stale_at?(time)
      last_seen_at.blank? || last_seen_at <= time - LAST_SEEN_TOUCH_INTERVAL
    end

    def with_last_seen_lock_timeout
      connection = self.class.connection
      if connection.adapter_name.to_s.downcase.include?("postgresql")
        self.class.transaction(requires_new: true) do
          connection.execute("SET LOCAL lock_timeout = '#{LAST_SEEN_LOCK_TIMEOUT}'")
          return yield
        end
      end

      yield
    end

    def last_seen_lock_timeout_error?(error)
      return true if error.is_a?(ActiveRecord::LockWaitTimeout)

      error.message.to_s.downcase.include?("lock timeout")
    end

  def normalize_provisioning_status
    self.provisioning_status = PROVISIONING_FAILED if provisioning_status.blank? || provisioning_status == "pending_manual"
  end

  def normalize_capabilities_json
    self.capabilities = capabilities
  end

  def labels_json_is_array
    parsed = JSON.parse(labels_json.presence || "[]")
    unless parsed.is_a?(Array)
      errors.add(:labels_json, "must decode to an array")
      return
    end

    invalid = parsed.filter_map do |entry|
      label = entry.to_s.strip
      label unless LABELS.include?(label)
    end
    errors.add(:labels_json, "contains unsupported labels") if invalid.any?
  rescue JSON::ParserError
    errors.add(:labels_json, "must be valid JSON")
  end
end
