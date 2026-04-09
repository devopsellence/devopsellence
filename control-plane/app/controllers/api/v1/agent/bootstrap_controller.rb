# frozen_string_literal: true

module Api
  module V1
    module Agent
      class BootstrapController < Api::V1::Agent::BaseController
        def create
          raw_token = params[:bootstrap_token].to_s
          name = params[:name].to_s.presence

          return render_error("invalid_request", "missing bootstrap_token") if raw_token.blank?

          bootstrap = NodeBootstrapToken.find_by_token(raw_token)
          return render_error("invalid_grant", "invalid token", status: :unauthorized) unless bootstrap

          node = nil
          access_token = nil
          refresh_token = nil
          target_environment = nil

          bootstrap.with_lock do
            return render_error("invalid_grant", "token expired", status: :unauthorized) unless bootstrap.active?
            return render_error("invalid_grant", "invalid bootstrap source", status: :unauthorized) unless valid_bootstrap_source?(bootstrap)

            bootstrap.consume!
            node, access_token, refresh_token = Node.issue!(
              organization: bootstrap.organization,
              name: name
            )
            apply_managed_attributes!(bootstrap, node)
            bootstrap.update!(node: node)
            target_environment = bootstrap.environment
          end

          assign_bootstrapped_node(node, environment: target_environment) if target_environment.present?
          persist_agent_capabilities!(node)

          render json: {
            node_id: node.id,
            organization_id: node.organization_id,
            access_token: access_token,
            refresh_token: refresh_token,
            token_type: "Bearer",
            expires_in: (node.access_expires_at - Time.current).to_i,
            desired_state_target: Nodes::DesiredStateTarget.for(node:, capabilities: current_agent_capabilities)
          }
        rescue Node::ProvisioningError => error
          render_error("server_error", error.message, status: :service_unavailable)
        end

        private

        def apply_managed_attributes!(bootstrap, node)
          return unless bootstrap.managed_pool_node?

          node.update!(
            managed: true,
            managed_provider: bootstrap.managed_provider,
            managed_region: bootstrap.managed_region,
            managed_size_slug: bootstrap.managed_size_slug,
            provider_server_id: bootstrap.provider_server_id,
            public_ip: bootstrap.public_ip,
            provisioning_status: Node::PROVISIONING_READY
          )
        end

        def valid_bootstrap_source?(bootstrap)
          return true unless bootstrap.managed_pool_node?
          return false if bootstrap.provider_server_id.blank?

          ActiveSupport::SecurityUtils.secure_compare(
            requested_provider_server_id,
            bootstrap.provider_server_id
          )
        rescue ArgumentError
          false
        end

        def requested_provider_server_id
          params[:provider_server_id].to_s
        end

        def assign_bootstrapped_node(node, environment:)
          Nodes::AssignmentManager.new(
            node: node,
            environment: environment,
            issuer: PublicBaseUrl.resolve(request)
          ).call
        rescue StandardError => error
          Rails.logger.warn(
            "[agent/bootstrap] deferring node assignment node_id=#{node.id} environment_id=#{environment.id} error=#{error.class}: #{error.message}"
          )
          Nodes::BootstrapAssignmentJob.perform_later(
            node_id: node.id,
            environment_id: environment.id,
            issuer: PublicBaseUrl.resolve(request)
          )
        end
      end
    end
  end
end
