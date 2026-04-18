# frozen_string_literal: true

module NodeDesiredState
  class ReleaseTaskBuilder
    def initialize(node:, environment:, release:, sequence:)
      @node = node
      @environment = environment
      @release = release
      @sequence = sequence
    end

    def call
      bundle = node.node_bundle
      revision = release.revision.presence || "node-#{node.id}-seq-#{sequence}"
      {
        schemaVersion: 2,
        revision: revision,
        assignmentSequence: sequence,
        identityVersion: environment.identity_version,
        environments: [
          {
            name: environment.name,
            revision: revision,
            tasks: [ release.release_task_for(node: node) ].compact
          }.compact
        ],
        publishedAt: Time.current.utc.iso8601,
        organizationBundleToken: bundle&.organization_bundle&.token.to_s,
        environmentBundleToken: bundle&.environment_bundle&.token.to_s,
        nodeBundleToken: bundle&.token.to_s
      }.compact
    end

    private

    attr_reader :node, :environment, :release, :sequence
  end
end
