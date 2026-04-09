# frozen_string_literal: true

module Gcp
  class RestClient
    SCOPE = "https://www.googleapis.com/auth/cloud-platform"

    def initialize
      @credentials = Credentials.new(scope: SCOPE)
    end

    def get(uri)
      request(parsed_uri(uri), Net::HTTP::Get.new(parsed_uri(uri)))
    end

    def post(uri, payload:)
      request(parsed_uri(uri), Net::HTTP::Post.new(parsed_uri(uri)), payload: payload)
    end

    def put(uri, payload:)
      request(parsed_uri(uri), Net::HTTP::Put.new(parsed_uri(uri)), payload: payload)
    end

    def delete(uri)
      request(parsed_uri(uri), Net::HTTP::Delete.new(parsed_uri(uri)))
    end

    private

    attr_reader :credentials

    def request(uri, request, payload: nil)
      request["Authorization"] = credentials.authorization_header
      request["Content-Type"] = "application/json" if payload
      request.body = JSON.generate(payload) if payload

      Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == "https") do |http|
        http.request(request)
      end
    end

    def parsed_uri(uri)
      uri.is_a?(URI::Generic) ? uri : URI.parse(uri)
    end
  end
end
