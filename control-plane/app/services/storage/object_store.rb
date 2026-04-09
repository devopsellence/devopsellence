# frozen_string_literal: true

require "cgi"
require "googleauth"
require "json"
require "net/http"
require "uri"

module Storage
  class ObjectStore
    SCOPE = "https://www.googleapis.com/auth/devstorage.read_write"

    def self.build
      runtime = Devopsellence::RuntimeConfig.current
      GcsStore.new(
        bucket: runtime.gcs_bucket,
        prefix: runtime.gcs_prefix,
        endpoint: runtime.gcs_endpoint
      )
    end

    class GcsStore
      def initialize(bucket:, prefix:, endpoint:)
        @bucket = bucket
        @prefix = prefix.to_s.gsub(%r{\A/+|/+\z}, "")
        @endpoint = endpoint.to_s.chomp("/")
      end

      def write_json!(object_path:, payload:, bucket: nil)
        write_json_batch!(entries: [ { object_path:, payload: } ], bucket:).first
      end

      def write_json_batch!(entries:, bucket: nil)
        resolved_bucket = bucket.presence || @bucket
        raise KeyError, "missing bucket" if resolved_bucket.blank?
        return [] if entries.empty?

        uris = []
        upload_uri = build_upload_uri(bucket: resolved_bucket, object_path: full_object_path(entries.first.fetch(:object_path)))
        http = Net::HTTP.start(upload_uri.host, upload_uri.port, use_ssl: upload_uri.scheme == "https")

        entries.each do |entry|
          object_path = entry.fetch(:object_path)
          full_path = full_object_path(object_path)
          uri = build_upload_uri(bucket: resolved_bucket, object_path: full_path)
          request = Net::HTTP::Post.new(uri)
          request["Content-Type"] = "application/json"
          request["Authorization"] = authorization_header
          request.body = JSON.generate(entry.fetch(:payload))

          response = http.request(request)
          ensure_success!(response)
          uris << "gs://#{resolved_bucket}/#{full_path}"
        end

        uris
      ensure
        if http
          http.finish if http.active?
        end
      end

      private

      def build_upload_uri(bucket:, object_path:)
        URI.parse("#{@endpoint}/upload/storage/v1/b/#{CGI.escape(bucket)}/o?uploadType=media&name=#{CGI.escape(object_path)}")
      end

      def ensure_success!(response)
        unless response.code.to_i.between?(200, 299)
          raise "gcs write failed (#{response.code}): #{response.body}"
        end
      end

      def full_object_path(object_path)
        [ @prefix, object_path ].reject(&:blank?).join("/")
      end

      def authorization_header
        @authorization_header ||= Gcp::Credentials.new(scope: SCOPE).authorization_header
      end
    end
  end
end
