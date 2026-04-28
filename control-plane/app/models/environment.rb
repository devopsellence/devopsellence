# frozen_string_literal: true

require "securerandom"

class Environment < ApplicationRecord
  ID_LENGTH = 12
  INGRESS_STRATEGY_DIRECT_DNS = "direct_dns"
  INGRESS_STRATEGIES = [
    INGRESS_STRATEGY_DIRECT_DNS
  ].freeze
  RUNTIME_CUSTOMER_NODES = "customer_nodes"
  RUNTIME_MANAGED = "managed"
  RUNTIME_KINDS = [
    RUNTIME_CUSTOMER_NODES,
    RUNTIME_MANAGED
  ].freeze

  belongs_to :project
  belongs_to :current_release, class_name: "Release", optional: true
  belongs_to :runtime_project, optional: true
  belongs_to :environment_bundle, optional: true

  has_many :deployments, dependent: :destroy
  has_many :environment_secrets, dependent: :destroy
  has_one :environment_ingress, dependent: :destroy
  has_many :node_bootstrap_tokens, dependent: :destroy
  has_many :nodes, dependent: :nullify

  before_validation :ensure_identity_material
  before_validation :apply_organization_runtime_defaults

  validates :name, presence: true
  validates :name, uniqueness: { scope: :project_id }
  validates :runtime_project, presence: true
  validates :gcp_project_id, presence: true, if: :gcp_runtime?
  validates :gcp_project_number, presence: true, if: :gcp_runtime?
  validates :workload_identity_pool, presence: true, if: :gcp_runtime?
  validates :workload_identity_provider, presence: true, if: :gcp_runtime?
  validates :identity_version, numericality: { greater_than: 0 }
  validates :runtime_kind, inclusion: { in: RUNTIME_KINDS }
  validates :ingress_strategy, inclusion: { in: INGRESS_STRATEGIES }

  def audience
    active_runtime_project.audience
  end

  def rotate_identity_version!
    update!(identity_version: identity_version + 1)
  end

  def managed_secret_refs_for(service_name)
    environment_secrets.where(service_name: service_name).each_with_object({}) do |secret, result|
      result[secret.name] = secret.secret_ref
    end
  end

  def managed_runtime?
    runtime_kind == RUNTIME_MANAGED
  end

  def customer_nodes_runtime?
    runtime_kind == RUNTIME_CUSTOMER_NODES
  end

  def direct_dns_ingress?
    ingress_strategy == INGRESS_STRATEGY_DIRECT_DNS
  end

  def assigned_ingress_nodes_missing_direct_dns_capability
    service_names = current_release&.ingress_target_service_names.to_a
    return [] if service_names.empty?

    nodes.select do |node|
      current_release.ingress_scheduled_on?(node) &&
        !node.supports_capability?(Node::CAPABILITY_DIRECT_DNS_INGRESS)
    end
  end


  def active_runtime_project
    runtime_project || project&.organization&.runtime_project || RuntimeProject.default!
  end

  def canonical_service_account_email
    raise "environment has no bundle" unless environment_bundle

    environment_bundle.service_account_email
  end

  private

  def gcp_runtime?
    active_runtime_project.gcp?
  end

  def ensure_identity_material
    if gcp_runtime?
      self.workload_identity_pool = random_id if workload_identity_pool.blank?
      self.workload_identity_provider = random_id if workload_identity_provider.blank?
    else
      self.workload_identity_pool = "" if workload_identity_pool.nil?
      self.workload_identity_provider = "" if workload_identity_provider.nil?
    end
  end

  def random_id
    # Prefix with a letter because some GCP IDs require leading alpha.
    "a#{SecureRandom.alphanumeric(ID_LENGTH - 1).downcase}"
  end

  def apply_organization_runtime_defaults
    return if project.blank? || project.organization.blank?

    organization = project.organization
    self.runtime_project ||= organization.runtime_project || RuntimeProject.default!
    runtime = active_runtime_project
    self.runtime_kind = organization_managed_default_runtime_kind if runtime_kind.blank?
    if runtime.gcp?
      self.gcp_project_id = runtime.gcp_project_id if gcp_project_id.blank?
      self.gcp_project_number = runtime.gcp_project_number if gcp_project_number.blank?
      self.workload_identity_pool = runtime.workload_identity_pool if workload_identity_pool.blank?
      self.workload_identity_provider = runtime.workload_identity_provider if workload_identity_provider.blank?
    else
      self.gcp_project_id = "" if gcp_project_id.nil?
      self.gcp_project_number = "" if gcp_project_number.nil?
      self.workload_identity_pool = "" if workload_identity_pool.nil?
      self.workload_identity_provider = "" if workload_identity_provider.nil?
    end
  end

  def organization_managed_default_runtime_kind
    self.class.column_defaults["runtime_kind"].presence || RUNTIME_MANAGED
  end
end
