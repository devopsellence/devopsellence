# frozen_string_literal: true

class Setup::GithubActionsController < ApplicationController
  DEFAULT_ENVIRONMENT_NAME = "production"

  layout "marketing"

  before_action :require_login

  def show
    @organization = session[:setup_organization]
    @project = session[:setup_project]
    @environment = session[:setup_environment]
    @token   = session.delete(:setup_token)

    if @token
      session.delete(:setup_organization)
      session.delete(:setup_project)
      session.delete(:setup_environment)
    end
  end

  def create
    organization_name = requested_organization_name
    project_name = params[:project_name].to_s.strip
    environment_name = requested_environment_name

    return redirect_to(setup_github_actions_path, alert: "Project name is required.") if project_name.blank?

    organization = resolve_organization(organization_name)
    return if performed?

    project = organization.projects.find_or_initialize_by(name: project_name)
    unless project.persisted? || project.save
      return redirect_to(setup_github_actions_path, alert: project.errors.full_messages.to_sentence)
    end

    environment = find_or_create_environment(project, organization, environment_name)
    return if performed?

    _record, raw = ApiToken.issue_ci_token!(user: current_user, name: "github-actions")

    session[:setup_organization] = organization.name
    session[:setup_token]   = raw
    session[:setup_project] = project.name
    session[:setup_environment] = environment.name

    redirect_to setup_github_actions_path
  end

  private

  def require_login
    return if signed_in?
    redirect_to login_path(redirect_path: setup_github_actions_path)
  end

  def requested_organization_name
    params[:organization_name].to_s.strip.presence || Organization::DEFAULT_NAME
  end

  def requested_environment_name
    params[:environment_name].to_s.strip.presence || DEFAULT_ENVIRONMENT_NAME
  end

  def resolve_organization(organization_name)
    existing = current_user.organizations.where(name: organization_name).order(:created_at).first
    return existing if existing

    if current_user.anonymous? && current_user.organizations.exists?
      return current_user.organizations.order(:created_at).first if organization_name == Organization::DEFAULT_NAME

      redirect_to setup_github_actions_path, alert: "Trial accounts support a single organization."
      return nil
    end

    org = Organization.create!(
      name: organization_name,
      plan_tier: current_user.anonymous? ? Organization::PLAN_TIER_TRIAL : Organization::PLAN_TIER_PAID
    )
    OrganizationMembership.create!(organization: org, user: current_user, role: OrganizationMembership::ROLE_OWNER)
    result = Gcp::OrganizationRuntimeProvisioner.new(organization: org).call
    if result.status == Organization::PROVISIONING_READY
      Runtime::EnsureBundles.enqueue
      return org
    end

    redirect_to setup_github_actions_path, alert: "Setup failed: #{result.message}"
    nil
  end

  def find_or_create_environment(project, organization, environment_name)
    existing = project.environments.where(name: environment_name).order(:created_at).first
    return existing if existing

    environment = project.environments.new(
      name: environment_name,
      gcp_project_id: organization.gcp_project_id,
      gcp_project_number: organization.gcp_project_number,
      workload_identity_pool: organization.workload_identity_pool,
      workload_identity_provider: organization.workload_identity_provider,
      runtime_kind: organization.trial? ? Environment::RUNTIME_MANAGED : Environment.column_defaults["runtime_kind"].presence || Environment::RUNTIME_MANAGED
    )

    unless environment.save
      redirect_to setup_github_actions_path, alert: environment.errors.full_messages.to_sentence
      return nil
    end

    result = Gcp::EnvironmentRuntimeProvisioner.new(environment: environment).call
    return environment if result.status == :ready

    redirect_to setup_github_actions_path, alert: "Environment setup failed: #{result.message}"
    nil
  end
end
