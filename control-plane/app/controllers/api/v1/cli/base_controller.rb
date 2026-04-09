# frozen_string_literal: true

module Api
  module V1
    module Cli
      class BaseController < Api::V1::BaseController
        private

        attr_reader :current_user_id, :current_api_token

        def member_organizations_scope
          Organization.joins(:organization_memberships)
            .where(organization_memberships: { user_id: current_user_id })
        end

        def owner_organizations_scope
          Organization.joins(:organization_memberships)
            .where(
              organization_memberships: {
                user_id: current_user_id,
                role: OrganizationMembership::ROLE_OWNER
              }
            )
        end

        def member_projects_scope
          Project.joins(:organization)
            .where(organizations: { id: member_organizations_scope.select(:id) })
        end

        def owner_projects_scope
          Project.joins(:organization)
            .where(organizations: { id: owner_organizations_scope.select(:id) })
        end

        def member_environments_scope
          Environment.joins(project: :organization)
            .where(organizations: { id: member_organizations_scope.select(:id) })
        end

        def owner_environments_scope
          Environment.joins(project: :organization)
            .where(organizations: { id: owner_organizations_scope.select(:id) })
        end

        def member_releases_scope
          Release.joins(project: :organization)
            .where(organizations: { id: member_organizations_scope.select(:id) })
        end

        def owner_releases_scope
          Release.joins(project: :organization)
            .where(organizations: { id: owner_organizations_scope.select(:id) })
        end

        def member_organization(id)
          member_organizations_scope.find_by(id: id)
        end

        def owner_organization(id)
          owner_organizations_scope.find_by(id: id)
        end

        def member_project(id)
          member_projects_scope.find_by(id: id)
        end

        def owner_project(id)
          owner_projects_scope.find_by(id: id)
        end

        def member_environment(id)
          member_environments_scope.find_by(id: id)
        end

        def owner_environment(id)
          owner_environments_scope.find_by(id: id)
        end

        def member_release(id)
          member_releases_scope.find_by(id: id)
        end

        def owner_release(id)
          owner_releases_scope.find_by(id: id)
        end

        def owner_membership_for?(organization)
          return false unless organization

          owner_organizations_scope.where(id: organization.id).exists?
        end

        def cli_rate_limit_key
          token = bearer_token
          return "ip:#{request.remote_ip}" unless token

          api_token = ApiToken.find_by(access_token_digest: ApiToken.digest(token))
          return "ip:#{request.remote_ip}" unless api_token

          "user:#{api_token.user_id}"
        end

        def render_rate_limited
          render_error("rate_limited", "too many requests", status: :too_many_requests)
        end

        def authenticate_cli_access!
          token = bearer_token
          return render_error("invalid_request", "missing bearer token", status: :unauthorized) unless token

          api_token = ApiToken.find_by(access_token_digest: ApiToken.digest(token))
          return render_error("invalid_grant", "invalid access_token", status: :unauthorized) unless api_token
          return render_error("invalid_grant", "access_token expired", status: :unauthorized) unless api_token.access_active?

          api_token.touch_last_used_at_if_stale!
          @current_user_id = api_token.user_id
          @current_api_token = api_token
        end

        def bearer_token
          scheme, value = request.authorization.to_s.split(" ", 2)
          return nil unless scheme&.casecmp("Bearer")&.zero?

          value.to_s.presence
        end

        def current_user
          @current_user ||= User.find(current_user_id)
        end
      end
    end
  end
end
