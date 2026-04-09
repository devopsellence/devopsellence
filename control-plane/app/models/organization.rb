# frozen_string_literal: true

class Organization < ApplicationRecord
  DEFAULT_NAME = "default"
  PLAN_TIER_PAID = "paid"
  PLAN_TIER_TRIAL = "trial"
  PLAN_TIERS = [
    PLAN_TIER_PAID,
    PLAN_TIER_TRIAL
  ].freeze
  PROVISIONING_READY = "ready"
  PROVISIONING_FAILED = "failed"
  PROVISIONING_STATUSES = [
    PROVISIONING_READY,
    PROVISIONING_FAILED
  ].freeze

  has_many :organization_memberships, dependent: :destroy
  has_many :users, through: :organization_memberships
  has_many :nodes, dependent: :restrict_with_error
  has_many :projects, dependent: :destroy
  has_many :node_bootstrap_tokens, dependent: :restrict_with_error
  has_many :organization_workload_identities, dependent: :destroy
  has_one :organization_registry_config, dependent: :destroy
  belongs_to :runtime_project, optional: true
  belongs_to :organization_bundle, optional: true

  before_validation :apply_runtime_defaults

  validates :name, presence: true
  validates :runtime_project, presence: true
  validates :gcp_project_id, presence: true, if: :gcp_runtime?
  validates :gcp_project_number, presence: true, if: :gcp_runtime?
  validates :workload_identity_pool, presence: true, if: :gcp_runtime?
  validates :workload_identity_provider, presence: true, if: :gcp_runtime?
  validates :gar_repository_region, presence: true, if: :gcp_runtime?
  validates :plan_tier, inclusion: { in: PLAN_TIERS }
  validates :provisioning_status, inclusion: { in: PROVISIONING_STATUSES }

  def owner?(user)
    organization_memberships.exists?(user: user, role: OrganizationMembership::ROLE_OWNER)
  end

  def audience
    active_runtime_project.audience
  end

  def trial?
    plan_tier == PLAN_TIER_TRIAL
  end

  def paid?
    plan_tier == PLAN_TIER_PAID
  end

  def gar_repository_path
    return organization_registry_config.repository_path if standalone_runtime? && organization_registry_config
    return organization_bundle.gar_repository_path if organization_bundle
    if Devopsellence::RuntimeConfig.current.gar_host_override.present?
      return "#{Devopsellence::RuntimeConfig.current.gar_host_override}/#{gcp_project_id}/#{gar_repository_name}"
    end

    "#{gar_repository_region}-docker.pkg.dev/#{gcp_project_id}/#{gar_repository_name}"
  end

  def active_runtime_project
    runtime_project || RuntimeProject.default!
  end

  private

  def gcp_runtime?
    active_runtime_project.gcp?
  end

  def standalone_runtime?
    active_runtime_project.standalone?
  end

  def apply_runtime_defaults
    self.runtime_project ||= RuntimeProject.default!
    runtime = active_runtime_project

    if runtime.gcp?
      self.gcp_project_id = runtime.gcp_project_id if gcp_project_id.blank?
      self.gcp_project_number = runtime.gcp_project_number if gcp_project_number.blank?
      self.workload_identity_pool = runtime.workload_identity_pool if workload_identity_pool.blank?
      self.workload_identity_provider = runtime.workload_identity_provider if workload_identity_provider.blank?
      if gar_repository_region.blank? || gar_repository_region == self.class.column_defaults["gar_repository_region"]
        self.gar_repository_region = runtime.gar_region
      end
    else
      self.gcp_project_id = "" if gcp_project_id.nil?
      self.gcp_project_number = "" if gcp_project_number.nil?
      self.workload_identity_pool = "" if workload_identity_pool.nil?
      self.workload_identity_provider = "" if workload_identity_provider.nil?
      self.gar_repository_region = "" if gar_repository_region.nil?
    end
    self.plan_tier = PLAN_TIER_PAID if plan_tier.blank?
    self.provisioning_status = PROVISIONING_FAILED if provisioning_status.blank? || provisioning_status == "pending_manual"
  end
end
