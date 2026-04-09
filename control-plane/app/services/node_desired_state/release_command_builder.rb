# frozen_string_literal: true

module NodeDesiredState
  class ReleaseCommandBuilder
    def initialize(node:, environment:, release:, sequence:)
      @node = node
      @environment = environment
      @release = release
      @sequence = sequence
    end

    def call
      bundle = node.node_bundle
      {
        revision: release.revision.presence || "node-#{node.id}-seq-#{sequence}",
        assignment_sequence: sequence,
        identity_version: environment.identity_version,
        releaseCommand: release.release_command_task_for(node: node),
        published_at: Time.current.utc.iso8601,
        organization_bundle_token: bundle&.organization_bundle&.token.to_s,
        environment_bundle_token: bundle&.environment_bundle&.token.to_s,
        node_bundle_token: bundle&.token.to_s
      }.compact
    end

    private

    attr_reader :node, :environment, :release, :sequence
  end
end
