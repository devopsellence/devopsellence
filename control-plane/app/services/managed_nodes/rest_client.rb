# frozen_string_literal: true

require "json"
require "net/http"
require "uri"

module ManagedNodes
  class RestClient
    def initialize(base_url:, token:)
      @base_url = base_url.to_s.sub(%r{/*$}, "")
      @token = token.to_s.strip
    end

    def get(path)
      request(:get, path)
    end

    def post(path, payload:)
      request(:post, path, payload: payload)
    end

    def delete(path)
      request(:delete, path)
    end

    private

    attr_reader :base_url, :token

    def request(method, path, payload: nil)
      uri = URI.parse("#{base_url}#{path}")
      request = request_class(method).new(uri)
      request["Authorization"] = "Bearer #{token}"
      request["Content-Type"] = "application/json" if payload
      request.body = JSON.generate(payload) if payload

      Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == "https") do |http|
        http.request(request)
      end
    end

    def request_class(method)
      case method
      when :get then Net::HTTP::Get
      when :post then Net::HTTP::Post
      when :delete then Net::HTTP::Delete
      else
        raise ArgumentError, "unsupported method #{method.inspect}"
      end
    end
  end
end
