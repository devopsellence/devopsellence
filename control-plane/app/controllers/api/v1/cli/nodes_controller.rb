# frozen_string_literal: true

module Api
  module V1
    module Cli
      class NodesController < Api::V1::Cli::BaseController
        before_action :authenticate_cli_access!

        def index
          organization = current_user.organizations.find_by(id: params[:organization_id])
          return render_error("not_found", "organization not found", status: :not_found) unless organization

          nodes = organization.nodes.includes(environment: :project).order(:created_at)

          render json: {
            nodes: nodes.map { |node| serialize(node) }
          }
        end

        def create_bootstrap_token
          organization = current_user.owned_organizations.find_by(id: params[:organization_id])
          return render_error("forbidden", "owner role required", status: :forbidden) unless organization
          return render_error("forbidden", "manual node management is unavailable for trial organizations", status: :forbidden) if organization.trial?

          environment = nil
          if params[:environment_id].present?
            environment = Environment.joins(:project).where(project: { organization: organization }).find_by(id: params[:environment_id])
            return render_error("not_found", "environment not found", status: :not_found) unless environment
          end

          NodeBootstrapToken.revoke_active_for(organization)
          record, raw_token = NodeBootstrapToken.issue!(
            organization: organization,
            environment: environment,
            issued_by_user: current_user
          )

          render json: {
            organization: {
              id: organization.id,
              name: organization.name
            },
            token: raw_token,
            assignment_mode: environment.present? ? "environment" : "unassigned",
            expires_at: record.expires_at.utc.iso8601,
            agent_image: AgentReleases::ContainerImage.metadata,
            install_command: %(curl -fsSL #{PublicBaseUrl.resolve(request)}/install.sh | bash -s -- --token "#{raw_token}")
          }, status: :created
        end

        def destroy
          node = Node.joins(:organization)
            .where(organizations: { id: current_user.owned_organizations.select(:id) })
            .find_by(id: params[:id])
          return render_error("forbidden", "owner role required", status: :forbidden) unless node
          return render_error("forbidden", "manual node management is unavailable for trial organizations", status: :forbidden) if node.organization&.trial?
          unless node.managed?
            return render_error(
              "invalid_request",
              "node remove is unsupported for customer-managed nodes; use node detach, then run devopsellence-agent uninstall --purge-runtime on the machine",
              status: :unprocessable_entity
            )
          end
          if node.environment_id.present?
            return render_error(
              "invalid_request",
              "node remove requires an unassigned managed node; use node detach first",
              status: :unprocessable_entity
            )
          end

          result = Nodes::Cleanup.new(node: node).call

          render json: {
            id: node.id,
            organization_id: node.organization_id,
            environment_id: result.environment&.id,
            desired_state_uri: result.desired_state&.uri,
            revoked_at: node.revoked_at&.utc&.iso8601
          }
        end

        def update_labels
          node = Node.joins(:organization)
            .where(organizations: { id: current_user.owned_organizations.select(:id) })
            .find_by(id: params[:id])
          return render_error("forbidden", "owner role required", status: :forbidden) unless node
          return render_error("forbidden", "manual node management is unavailable for trial organizations", status: :forbidden) if node.organization&.trial?

          node.labels = params[:labels].to_s.split(/[,\s]+/).map(&:strip).reject(&:empty?).uniq
          unless node.save
            return render_error("invalid_request", node.errors.full_messages.to_sentence, status: :unprocessable_entity)
          end

          render json: serialize(node)
        end

        private

        def serialize(node)
          environment = node.environment
          {
            id: node.id,
            name: node.name,
            organization_id: node.organization_id,
            labels: node.labels,
            managed: node.managed,
            public_ip: node.public_ip,
            provisioning_status: node.provisioning_status,
            desired_state_bucket: node.desired_state_bucket,
            desired_state_object_path: node.desired_state_object_path,
            desired_state_uri: node.desired_state_uri,
            revoked_at: node.revoked_at&.utc&.iso8601,
            environment: environment && {
              id: environment.id,
              name: environment.name,
              project_id: environment.project_id,
              project_name: environment.project.name
            }
          }
        end
      end
    end
  end
end
