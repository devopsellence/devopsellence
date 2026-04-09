# frozen_string_literal: true

module Api
  module V1
    module Cli
      class OrganizationsController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!

        def index
          memberships = current_user.organization_memberships.includes(:organization).order(created_at: :asc)

          render json: {
            organizations: memberships.map do |membership|
              serialize(membership.organization, role: membership.role)
            end
          }
        end

        def create
          name = params[:name].to_s.strip
          return render_error("invalid_request", "name is required", status: :unprocessable_entity) if name.blank?
          if current_user.anonymous? && current_user.organizations.exists?
            return render_error("forbidden", "trial accounts support a single organization", status: :forbidden)
          end

          organization = Organization.create!(name: name, plan_tier: current_user.anonymous? ? Organization::PLAN_TIER_TRIAL : Organization::PLAN_TIER_PAID)
          membership = OrganizationMembership.create!(
            organization: organization,
            user: current_user,
            role: OrganizationMembership::ROLE_OWNER
          )
          result = Gcp::OrganizationRuntimeProvisioner.new(organization: organization).call
          return render_error("provisioning_failed", result.message, status: :unprocessable_entity) unless result.status == Organization::PROVISIONING_READY

          Runtime::EnsureBundles.enqueue

          render json: serialize(organization, role: membership.role), status: :created
        rescue ActiveRecord::RecordInvalid => error
          render_error("invalid_request", error.record.errors.full_messages.to_sentence, status: :unprocessable_entity)
        end

        private

        def serialize(organization, role:)
          {
            id: organization.id,
            name: organization.name,
            role: role,
            plan_tier: organization.plan_tier
          }
        end
      end
    end
  end
end
