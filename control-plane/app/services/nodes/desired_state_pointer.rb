# frozen_string_literal: true

module Nodes
  class DesiredStatePointer
    FORMAT = "desired_state_pointer.v1"
    CAPABILITY = FORMAT
    SCHEMA_VERSION = 1
    POINTER_FILENAME = "desired_state_pointer.json"

    class << self
      def build(sequence:, object_path:, published_at: Time.current)
        {
          format: FORMAT,
          schema_version: SCHEMA_VERSION,
          sequence: sequence,
          object_path: object_path,
          published_at: published_at.utc.iso8601
        }
      end

      def pointer_object_path(reference_path:)
        base_dir = reference_path.to_s.split("/")[0...-1].join("/")
        [base_dir.presence, POINTER_FILENAME].compact.join("/")
      end

      def pointer_uri(bucket:, reference_path:)
        return nil if bucket.blank?

        object_path = pointer_object_path(reference_path:)
        return nil if object_path.blank?

        "gs://#{bucket}/#{object_path}"
      end

      def sequence_object_path(reference_path:, sequence:)
        base_dir = reference_path.to_s.split("/")[0...-1].join("/")
        [
          base_dir.presence,
          "desired-states",
          format("%012d.json", sequence)
        ].compact.join("/")
      end
    end
  end
end
