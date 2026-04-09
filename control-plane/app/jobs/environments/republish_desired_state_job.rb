# frozen_string_literal: true

module Environments
  class RepublishDesiredStateJob < ApplicationJob
    queue_as :default

    def perform(environment_id)
      environment = Environment.includes(:current_release, :nodes).find_by(id: environment_id)
      return unless environment&.current_release

      standalone = environment.active_runtime_project&.standalone?

      environment.nodes.order(:created_at).each do |node|
        unless standalone
          next if node.desired_state_bucket.to_s.strip.empty?
        end
        next if node.desired_state_object_path.to_s.strip.empty?

        Nodes::DesiredStatePublisher.new(node:, release: environment.current_release).call
      end
    end
  end
end
