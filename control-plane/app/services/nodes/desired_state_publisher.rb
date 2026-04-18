# frozen_string_literal: true

require "digest"
require "json"

module Nodes
  class DesiredStatePublisher
    Result = Struct.new(:sequence, :uri, :payload, keyword_init: true)

    def self.unassigned_payload(node:)
      bundle = node.node_bundle
      {
        schemaVersion: 2,
        assigned: false,
        revision: "unassigned-node-bundle-#{bundle.token}",
        sequence: node.desired_state_sequence,
        identityVersion: 0,
        desiredStateBucket: node.desired_state_bucket,
        desiredStateObjectPath: node.desired_state_object_path,
        environments: [],
        publishedAt: Time.current.utc.iso8601,
        organizationBundleToken: bundle.organization_bundle.token,
        environmentBundleToken: bundle.environment_bundle.token,
        nodeBundleToken: bundle.token
      }
    end

    def initialize(node:, release: nil, payload: nil, store: Storage::ObjectStore.build)
      @node = node
      @release = release
      @payload = payload
      @store = store
    end

    def self.unassigned_envelope(node:)
      payload = unassigned_payload(node:)
      DesiredStateEnvelope.wrap(
        node: node,
        environment: nil,
        sequence: node.desired_state_sequence,
        payload: payload
      )
    end

    def call
      return publish_unassigned unless assigned_environment

      sequence = next_sequence
      payload = desired_state_payload(sequence:)
      envelope = DesiredStateEnvelope.wrap(node: node, environment: assigned_environment, sequence: sequence, payload: payload)
      uri = publish_documents!(sequence:, envelope:)
      persist_assignment_state!(sequence)
      prune_standalone_documents!(keep_sequence: sequence)

      Result.new(sequence:, uri:, payload: envelope)
    end

    private

    attr_reader :node, :payload, :store

    def desired_state_payload(sequence:)
      return payload.call(sequence:) if payload.respond_to?(:call)
      return self.class.unassigned_payload(node: node).merge(sequence: sequence) unless active_release

      NodeDesiredState::Builder.new(
        node: node,
        environment: assigned_environment,
        release: active_release,
        sequence: sequence
      ).call.merge(
        assigned: true,
        desiredStateBucket: node.desired_state_bucket,
        desiredStateObjectPath: node.desired_state_object_path
      )
    end

    def next_sequence
      current_sequence + 1
    end

    def persist_assignment_state!(sequence)
      node.update!(desired_state_sequence: sequence) if node.desired_state_sequence != sequence

      bundle = node.node_bundle
      bundle.update!(desired_state_sequence: sequence) if bundle && bundle.desired_state_sequence != sequence
    end

    def assigned_environment
      @assigned_environment ||= node.environment
    end

    def active_release
      @active_release ||= @release || assigned_environment&.current_release
    end

    def current_sequence
      node.desired_state_sequence
    end

    def publish_unassigned
      sequence = current_sequence
      envelope = self.class.unassigned_envelope(node: node)
      return Result.new(sequence:, uri: nil, payload: envelope) if node.desired_state_uri.blank?

      uri = publish_documents!(sequence:, envelope:)
      persist_assignment_state!(sequence)
      prune_standalone_documents!(keep_sequence: sequence)
      Result.new(sequence:, uri:, payload: envelope)
    end

    def publish_documents!(sequence:, envelope:)
      return publish_standalone_document!(sequence:, envelope:) if node.node_bundle&.runtime_project&.standalone?

      immutable_object_path = Nodes::DesiredStatePointer.sequence_object_path(
        reference_path: node.desired_state_object_path,
        sequence: sequence
      )
      pointer = Nodes::DesiredStatePointer.build(sequence:, object_path: immutable_object_path)
      pointer_object_path = Nodes::DesiredStatePointer.pointer_object_path(reference_path: node.desired_state_object_path)
      store.write_json_batch!(
        bucket: node.desired_state_bucket,
        entries: [
          { object_path: immutable_object_path, payload: envelope },
          { object_path: node.desired_state_object_path, payload: envelope },
          { object_path: pointer_object_path, payload: pointer }
        ]
      )

      node.desired_state_uri
    end

    def publish_standalone_document!(sequence:, envelope:)
      payload_json = JSON.generate(envelope)
      sha256 = Digest::SHA256.hexdigest(payload_json)
      document = StandaloneDesiredStateDocument.find_or_initialize_by(node: node, sequence: sequence)
      document.assign_attributes(
        node_bundle: node.node_bundle,
        environment: assigned_environment,
        etag: sha256,
        sha256: sha256,
        payload_json: payload_json
      )
      document.save!
      node.desired_state_uri
    end

    def prune_standalone_documents!(keep_sequence:)
      return unless node.node_bundle&.runtime_project&.standalone?

      StandaloneDesiredStateDocument
        .where(node: node)
        .where("sequence < ?", keep_sequence - 1)
        .delete_all
    end
  end
end
