# frozen_string_literal: true

require "json"
require "securerandom"

class DashboardController < ApplicationController
  prepend_before_action :redirect_to_getting_started
  skip_forgery_protection

  def index
    @public_base_url = PublicBaseUrl.resolve(request)
    @agent_stable_version = Devopsellence::RuntimeConfig.current.agent_stable_version.presence
    @memberships = current_user.organization_memberships.includes(:organization).order(created_at: :asc)
    @organizations = @memberships.map(&:organization)
    @selected_organization = select_organization(@organizations)
    @selected_membership = @memberships.find { |membership| membership.organization_id == @selected_organization&.id }
    @owner_selected = @selected_membership&.owner? || false
    @raw_token = params[:token].to_s.presence
    @projects = @selected_organization&.projects&.includes(:environments)&.order(created_at: :asc) || []
    @selected_project = select_project(@projects)
    @environments = @selected_project&.environments&.order(created_at: :asc) || []
    @selected_environment = select_environment(@environments)
    @nodes = @selected_organization&.nodes&.includes(environment: :project)&.order(created_at: :asc) || []
    @releases = @selected_project&.releases&.order(created_at: :desc) || []
    @deployments = @selected_environment&.deployments&.includes(:release)&.order(created_at: :desc) || []
    @environment_secrets = @selected_environment&.environment_secrets&.order(:service_name, :name) || []
    @secret_service_names = secret_service_names(@selected_environment)
    @default_env_json = default_env_json
    @default_secret_refs_json = default_secret_refs_json
  end

  def create_organization
    name = params[:name].to_s.strip
    return redirect_to(dashboard_path, alert: "Organization name is required.") if name.blank?

    organization = Organization.create!(name: name)
    OrganizationMembership.create!(organization: organization, user: current_user, role: OrganizationMembership::ROLE_OWNER)
    result = Gcp::OrganizationRuntimeProvisioner.new(organization: organization).call
    return redirect_to(dashboard_path(organization_id: organization.id), alert: "Organization provisioning failed: #{result.message}") unless result.status == Organization::PROVISIONING_READY

    Runtime::EnsureBundles.enqueue

    redirect_to dashboard_path(organization_id: organization.id), notice: "Organization created."
  end

  def bootstrap_node
    organization = owned_organization(params[:organization_id])
    return redirect_to(dashboard_path, alert: "Owner role required.") unless organization

    NodeBootstrapToken.revoke_active_for(organization)
    _record, raw = NodeBootstrapToken.issue!(organization: organization, issued_by_user: current_user)

    redirect_to dashboard_path(organization_id: organization.id, token: raw), notice: "New install token generated."
  end

  def create_project
    organization = owned_organization(params[:organization_id])
    return redirect_to(dashboard_path, alert: "Owner role required.") unless organization

    name = params[:name].to_s.strip
    return redirect_to(dashboard_path(organization_id: organization.id), alert: "Project name is required.") if name.blank?
    project = organization.projects.new(name: name)
    unless project.save
      return redirect_to(dashboard_path(organization_id: organization.id), alert: project.errors.full_messages.to_sentence)
    end

    redirect_to dashboard_path(organization_id: organization.id, project_id: project.id), notice: "Project created."
  end

  def create_environment
    project = owned_project(params[:project_id])
    return redirect_to(dashboard_path, alert: "Owner role required.") unless project

    organization = project.organization
    name = params[:name].to_s.strip
    return redirect_to(dashboard_path(organization_id: organization.id, project_id: project.id), alert: "Environment name is required.") if name.blank?
    if params[:service_account_email].present?
      return redirect_to(
        dashboard_path(organization_id: organization.id, project_id: project.id),
        alert: "service account email is managed by devopsellence."
      )
    end
    if organization.gcs_bucket_name.blank? || organization.gar_repository_name.blank?
      result = Gcp::OrganizationRuntimeProvisioner.new(organization: organization).call
      return redirect_to(
        dashboard_path(organization_id: organization.id, project_id: project.id),
        alert: "Organization provisioning failed: #{result.message}"
      ) unless result.status == Organization::PROVISIONING_READY
    end

    runtime_kind = params[:runtime_kind].to_s.strip.presence || Environment.column_defaults["runtime_kind"].presence || Environment::RUNTIME_MANAGED
    environment = project.environments.new(
      name: name,
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: runtime_kind
    )
    unless environment.save
      return redirect_to(
        dashboard_path(organization_id: organization.id, project_id: project.id),
        alert: environment.errors.full_messages.to_sentence
      )
    end

    result = Gcp::EnvironmentRuntimeProvisioner.new(environment: environment).call
    return redirect_to(dashboard_path(organization_id: organization.id, project_id: project.id), alert: result.message) unless result.status == :ready

    redirect_to dashboard_path(organization_id: organization.id, project_id: project.id, environment_id: environment.id), notice: "Environment created."
  end

  def assign_node
    environment = owned_environment(params[:environment_id])
    return redirect_to(dashboard_path, alert: "Owner role required.") unless environment

    organization = environment.project.organization
    node = organization.nodes.find_by(id: params[:node_id])
    return redirect_to(dashboard_path(organization_id: organization.id, project_id: environment.project_id, environment_id: environment.id), alert: "Node not found.") unless node

    Nodes::AssignmentManager.new(
      node: node,
      environment: environment,
      issuer: PublicBaseUrl.resolve(request)
    ).call

    redirect_to dashboard_path(organization_id: organization.id, project_id: environment.project_id, environment_id: environment.id), notice: "Node assigned."
  rescue Nodes::AssignmentManager::Error => error
    redirect_to dashboard_path(organization_id: organization.id, project_id: environment.project_id, environment_id: environment.id), alert: "Assignment failed: #{error.message}"
  end

  def update_node_labels
    node = Node.joins(:organization).where(organizations: { id: current_user.owned_organizations.select(:id) }).find_by(id: params[:node_id])
    return redirect_to(dashboard_path, alert: "Owner role required.") unless node

    labels = params[:labels].to_s.split(/[,\s]+/).map(&:strip).reject(&:empty?).uniq
    node.labels = labels
    unless node.save
      return redirect_to(dashboard_path(organization_id: node.organization_id), alert: node.errors.full_messages.to_sentence)
    end

    redirect_to(dashboard_path(organization_id: node.organization_id), notice: "Node labels updated.")
  end

  def upsert_environment_secret
    environment = owned_environment(params[:environment_id])
    return redirect_to(dashboard_path, alert: "Owner role required.") unless environment

    service_name = EnvironmentSecret.normalize_service_name_value(params[:service_name])
    name = params[:name].to_s.strip
    value = params[:value].to_s
    return redirect_to(secret_dashboard_path(environment), alert: "Secret value is required.") if value.blank?

    environment_secret = environment.environment_secrets.find_or_initialize_by(service_name: service_name, name: name)
    Gcp::EnvironmentSecretManager.new.upsert!(environment_secret: environment_secret, value: value)

    redirect_to(secret_dashboard_path(environment), notice: "Secret saved for #{service_name}.")
  rescue ActiveRecord::RecordInvalid => error
    redirect_to(secret_dashboard_path(environment), alert: error.record.errors.full_messages.to_sentence)
  rescue StandardError => error
    redirect_to(secret_dashboard_path(environment), alert: "Secret save failed: #{error.message}")
  end

  def create_release
    project = owned_project(params[:project_id])
    return redirect_to(dashboard_path, alert: "Owner role required.") unless project

    organization = project.organization
    release = project.releases.new(release_runtime_attributes)
    unless release.save
      return redirect_to(dashboard_path(organization_id: organization.id, project_id: project.id), alert: release.errors.full_messages.to_sentence)
    end

    redirect_to dashboard_path(organization_id: organization.id, project_id: project.id), notice: "Release created."
  rescue Releases::RuntimeAttributes::InvalidPayload => error
    redirect_to dashboard_path(organization_id: organization.id, project_id: project.id), alert: error.message
  end

  def publish_release
    release = owned_release(params[:release_id])
    return redirect_to(dashboard_path, alert: "Owner role required.") unless release

    environment = release.project.environments.find_by(id: params[:environment_id])
    unless environment
      return redirect_to(
        dashboard_path(organization_id: release.project.organization_id, project_id: release.project_id),
        alert: "Environment not found for this release."
      )
    end

    result = Deployments::Scheduler.new(
      environment: environment,
      release: release,
      request_token: SecureRandom.hex(16)
    ).call
    redirect_to(
      dashboard_path(organization_id: release.project.organization_id, project_id: release.project_id, environment_id: environment.id),
      notice: "Release scheduled for environment ##{environment.id} (deployment ##{result.deployment.id})."
    )
  rescue StandardError => error
    redirect_to(
      dashboard_path(organization_id: release.project.organization_id, project_id: release.project_id, environment_id: params[:environment_id]),
      alert: "Publish failed: #{error.message}"
    )
  end

  private

  def redirect_to_getting_started
    redirect_to getting_started_path(anchor: "quickstart-heading"), status: :see_other
  end

  def require_login
    return if signed_in?

    redirect_to login_path, alert: "Please sign in."
  end

  def select_organization(organizations)
    requested_id = params[:organization_id].to_i
    selected = organizations.find { |organization| organization.id == requested_id }
    selected || organizations.first
  end

  def select_project(projects)
    requested_id = params[:project_id].to_i
    selected = projects.find { |project| project.id == requested_id }
    selected || projects.first
  end

  def select_environment(environments)
    requested_id = params[:environment_id].to_i
    selected = environments.find { |environment| environment.id == requested_id }
    selected || environments.first
  end

  def owned_organization(id)
    current_user.owned_organizations.find_by(id: id)
  end

  def owned_project(id)
    Project.joins(:organization).where(organizations: { id: current_user.owned_organizations.select(:id) }).find_by(id: id)
  end

  def owned_environment(id)
    Environment.joins(project: :organization).where(organizations: { id: current_user.owned_organizations.select(:id) }).find_by(id: id)
  end

  def owned_release(id)
    Release.joins(project: :organization).where(organizations: { id: current_user.owned_organizations.select(:id) }).find_by(id: id)
  end

  def release_runtime_attributes
    Releases::RuntimeAttributes.new(
      params: {
        git_sha: params[:git_sha],
        image_repository: params[:image_repository],
        image_digest: params[:image_digest],
        revision: params[:revision],
        services: params[:services],
        tasks: params[:tasks],
        ingress_service: params[:ingress_service],
        healthcheck_interval_seconds: params[:healthcheck_interval_seconds],
        healthcheck_timeout_seconds: params[:healthcheck_timeout_seconds]
      }
    ).to_h
  end

  def default_env_json
    JSON.pretty_generate(
      {
        "RAILS_ENV" => "production"
      }
    )
  end

  def default_secret_refs_json
    JSON.pretty_generate(
      [
        {
          name: "DATABASE_URL",
          secret: "projects/example/secrets/database-url/versions/latest"
        }
      ]
    )
  end

  def secret_service_names(environment)
    names = Array(environment&.current_release&.service_names) +
      Array(environment&.environment_secrets&.pluck(:service_name))
    names.map(&:to_s).map(&:strip).reject(&:blank?).uniq.sort
  end

  def secret_dashboard_path(environment)
    dashboard_path(
      organization_id: environment.project.organization_id,
      project_id: environment.project_id,
      environment_id: environment.id
    )
  end
end
