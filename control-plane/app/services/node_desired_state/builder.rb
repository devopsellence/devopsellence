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
      revision = release.revision.presence || "node-#{node.id}-seq-#{sequence}"
      {
        schemaVersion: 2,
        revision: revision,
        assignmentSequence: sequence,
        identityVersion: environment.identity_version,
        image: {
          repository: release.image_repository,
          digest: release.image_digest,
          reference: release.image_reference_for(organization)
        },
        environments: [
          {
            name: environment.name,
            revision: revision,
            services: release.scheduled_services_for(node: node)
          }.compact
        ],
        ingress: ingress,
        nodePeers: node_peers_payload(environment:, node:),
        publishedAt: Time.current.utc.iso8601,
        organizationBundleToken: bundle&.organization_bundle&.token.to_s,
        environmentBundleToken: bundle&.environment_bundle&.token.to_s,
        nodeBundleToken: bundle&.token.to_s
      }.compact
    end

    private

    attr_reader :node, :environment, :release, :sequence

    def ingress_payload(environment:, node:)
      IngressPayload.build(node:, environment:, release:)
    end

    def node_peers_payload(environment:, node:)
      NodePeersPayload.build(node:, environment:)
    end
  end
end
