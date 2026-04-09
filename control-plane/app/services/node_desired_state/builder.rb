# frozen_string_literal: true

module NodeDesiredState
  class Builder
    def initialize(node:, environment:, release:, sequence:)
      @node = node
      @environment = environment
      @release = release
      @sequence = sequence
    end

    def call
      organization = environment.project.organization
      ingress = ingress_payload(environment:, node:)

      bundle = node.node_bundle
      {
        revision: release.revision.presence || "node-#{node.id}-seq-#{sequence}",
        assignment_sequence: sequence,
        identity_version: environment.identity_version,
        image: {
          repository: release.image_repository,
          digest: release.image_digest,
          reference: release.image_reference_for(organization)
        },
        containers: release.scheduled_containers_for(node: node),
        ingress: ingress,
        published_at: Time.current.utc.iso8601,
        organization_bundle_token: bundle&.organization_bundle&.token.to_s,
        environment_bundle_token: bundle&.environment_bundle&.token.to_s,
        node_bundle_token: bundle&.token.to_s
      }.compact
    end

    private

    attr_reader :node, :environment, :release, :sequence

    def ingress_payload(environment:, node:)
      IngressPayload.build(node:, environment:, release:)
    end
  end
end
