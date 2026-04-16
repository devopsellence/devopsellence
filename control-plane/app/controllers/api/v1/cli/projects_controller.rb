# frozen_string_literal: true

module Api
  module V1
    module Cli
      class ProjectsController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!

        def index
          organization = member_organization(params[:organization_id])
          return render_error("not_found", "organization not found", status: :not_found) unless organization

          projects = organization.projects
          projects = projects.where(id: params[:id].to_i) if params[:id].present?

          render json: {
            projects: projects.order(:created_at).map do |project|
              { id: project.id, name: project.name, organization_id: project.organization_id }
            end
          }
        end

        def create
          organization = owner_organization(params[:organization_id])
          return render_error("forbidden", "owner role required", status: :forbidden) unless organization

          project = organization.projects.new(
            name: params[:name].to_s.strip
          )

          unless project.save
            return render_error("invalid_request", project.errors.full_messages.to_sentence, status: :unprocessable_entity)
          end

          render json: { id: project.id, name: project.name, organization_id: project.organization_id }, status: :created
        end

        def destroy
          project = owner_project(params[:id])
          return render_error("forbidden", "owner role required", status: :forbidden) unless project

          project.environments.find_each do |environment|
            Environments::Delete.new(environment:).call
          end

          unless project.destroy
            return render_error("invalid_request", project.errors.full_messages.to_sentence, status: :unprocessable_entity)
          end

          render json: {
            id: project.id,
            name: project.name,
            organization_id: project.organization_id,
            deleted: true
          }
        end
      end
    end
  end
end
