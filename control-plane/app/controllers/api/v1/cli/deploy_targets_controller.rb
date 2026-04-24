# frozen_string_literal: true

module Api
  module V1
    module Cli
      class DeployTargetsController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!
        rate_limit to: 10, within: 1.minute, name: "cli_deploy_target_create", by: :cli_rate_limit_key, with: :render_rate_limited, only: :create

        def create
          project_name = params[:project].to_s.strip
          environment_name = params[:environment].to_s.strip

          return render_error("invalid_request", "project is required", status: :unprocessable_entity) if project_name.blank?
          return render_error("invalid_request", "environment is required", status: :unprocessable_entity) if environment_name.blank?

          organization, organization_created = resolve_organization
          return if performed?

          project = nil
          project_created = false
          environment = nil
          environment_created = false
          owner_writes_allowed = organization_created || owner_membership_for?(organization)

          Runtime::AdvisoryLock.with_lock(deploy_target_lock_name(organization.id, project_name, environment_name)) do
            project = organization.projects.where(name: project_name).order(:id).first
            unless project
              unless owner_writes_allowed
                render_error("forbidden", "owner role required", status: :forbidden)
                next
              end

              project = organization.projects.new(name: project_name)
              unless project.save
                render_error("invalid_request", project.errors.full_messages.to_sentence, status: :unprocessable_entity)
                next
              end
              project_created = true
            end

            environment = project.environments.where(name: environment_name).order(:id).first
            next if environment

            unless owner_writes_allowed
              render_error("forbidden", "owner role required", status: :forbidden)
              next
            end

            environment = create_environment(project, environment_name)
            environment_created = environment.present?
          end
          return if performed?

          if organization_created || project_created || environment_created
            Runtime::EnsureBundles.enqueue if environment.present?
          end

          render json: {
            organization: serialize_organization(organization),
            organization_created: organization_created,
            project: serialize_project(project),
            project_created: project_created,
            environment: serialize_environment(environment),
            environment_created: environment_created
          }, status: organization_created || project_created || environment_created ? :created : :ok
        end

        private

        def resolve_organization
          input = params[:organization].to_s.strip
          if input.present?
            organization = organization_by_input(input)
            return [ organization, false ] if organization

            return [ create_organization(input), true ] unless performed?

            return [ nil, false ]
          end

          organizations = member_organizations_scope.order(:created_at).to_a
          default_organization = organizations.find { |organization| organization.name == Organization::DEFAULT_NAME }
          return [ default_organization, false ] if default_organization

          if organizations.empty?
            return [ create_organization(Organization::DEFAULT_NAME), true ] unless performed?

            return [ nil, false ]
          end
          return [ organizations.first, false ] if organizations.one?

          preferred_id = params[:preferred_organization_id].to_i
          if preferred_id.positive?
            preferred = organizations.find { |organization| organization.id == preferred_id }
            return [ preferred, false ] if preferred
          end

          render_error("invalid_request", "multiple organizations available; pass organization or preferred_organization_id", status: :unprocessable_entity)
          [ nil, false ]
        end

        def organization_by_input(input)
          if integer_string?(input)
            member_organization(input.to_i)
          else
            member_organizations_scope.where(name: input).order(:created_at).first
          end
        end

        def create_organization(name)
          if current_user.anonymous? && member_organizations_scope.exists?
            render_error("forbidden", "trial accounts support a single organization", status: :forbidden)
            return nil
          end

          organization = Organization.create!(name: name, plan_tier: current_user.anonymous? ? Organization::PLAN_TIER_TRIAL : Organization::PLAN_TIER_PAID)
          OrganizationMembership.create!(
            organization: organization,
            user: current_user,
            role: OrganizationMembership::ROLE_OWNER
          )
          result = Gcp::OrganizationRuntimeProvisioner.new(organization: organization).call
          if result.status == Organization::PROVISIONING_READY
            Runtime::EnsureBundles.enqueue
            return organization
          end

          render_error("provisioning_failed", result.message, status: :unprocessable_entity)
          nil
        rescue ActiveRecord::RecordInvalid => error
          render_error("invalid_request", error.record.errors.full_messages.to_sentence, status: :unprocessable_entity)
          nil
        end

        def create_environment(project, name)
          organization = project.organization
          if organization.gcs_bucket_name.blank? || organization.gar_repository_name.blank?
            result = Gcp::OrganizationRuntimeProvisioner.new(organization: organization).call
            return render_error("provisioning_failed", result.message, status: :unprocessable_entity) unless result.status == Organization::PROVISIONING_READY
          end

          environment = project.environments.new(
            name: name,
            gcp_project_id: organization.gcp_project_id,
            gcp_project_number: organization.gcp_project_number,
            workload_identity_pool: organization.workload_identity_pool,
            workload_identity_provider: organization.workload_identity_provider,
            runtime_kind: organization.trial? ? Environment::RUNTIME_MANAGED : Environment.column_defaults["runtime_kind"].presence || Environment::RUNTIME_MANAGED
          )

          unless environment.save
            render_error("invalid_request", environment.errors.full_messages.to_sentence, status: :unprocessable_entity)
            return nil
          end

          result = Gcp::EnvironmentRuntimeProvisioner.new(environment: environment).call
          return environment if result.status == :ready

          render_error("provisioning_failed", result.message, status: :unprocessable_entity)
          nil
        end

        def serialize_organization(organization)
          {
            id: organization.id,
            name: organization.name,
            plan_tier: organization.plan_tier
          }
        end

        def serialize_project(project)
          {
            id: project.id,
            name: project.name,
            organization_id: project.organization_id
          }
        end

        def serialize_environment(environment)
          {
            id: environment.id,
            name: environment.name,
            project_id: environment.project_id,
            identity_version: environment.identity_version,
            runtime_kind: environment.runtime_kind,
            ingress_hosts: environment_ingress_hosts(environment)
          }
        end

        def environment_ingress_hosts(environment)
          ingress_hosts = environment.environment_ingress&.hosts
          return ingress_hosts if ingress_hosts.present?

          bundle_hostname = environment.environment_bundle&.hostname.to_s.strip
          return [ bundle_hostname ] if bundle_hostname.present?

          []
        end

        def integer_string?(value)
          /\A\d+\z/.match?(value)
        end

        def deploy_target_lock_name(organization_id, project_name, environment_name)
          "cli/deploy_target/#{organization_id}/#{project_name}/#{environment_name}"
        end
      end
    end
  end
end
