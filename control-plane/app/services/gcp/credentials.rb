# frozen_string_literal: true

require "googleauth"
require "json"
require "net/http"
require "time"
require "uri"

module Gcp
  class Credentials
    TOKEN_REFRESH_BUFFER_SECONDS = 60
    FAKE_ACCESS_TOKEN_ENV = "DEVOPSELLENCE_GCP_FAKE_ACCESS_TOKEN"

    def initialize(scope:)
      @scope = scope
    end

    def authorization_header
      "Bearer #{access_token}"
    end

    def access_token
      fake_access_token = ENV.fetch(FAKE_ACCESS_TOKEN_ENV, "").to_s.strip
      return fake_access_token if fake_access_token.present?

      return impersonated_access_token if impersonated_service_account_credentials?

      if authorization.respond_to?(:fetch_access_token)
        authorization.fetch_access_token.fetch("access_token")
      else
        metadata = {}
        apply!(metadata)
        metadata.fetch(:authorization, metadata.fetch("authorization")).delete_prefix("Bearer ")
      end
    end

    def fetch_access_token
      { "access_token" => access_token }
    end

    def apply!(metadata)
      metadata["authorization"] = authorization_header
      metadata[:authorization] = metadata["authorization"]
      metadata
    end

    private

    attr_reader :scope

    def impersonated_service_account_credentials?
      authorization.respond_to?(:source_credentials) &&
        authorization.respond_to?(:impersonation_url) &&
        authorization.respond_to?(:scope)
    end

    def impersonated_access_token
      return @impersonated_access_token if token_fresh?

      source_access_token = authorization.source_credentials.fetch_access_token.fetch("access_token")
      uri = URI.parse(authorization.impersonation_url)
      request = Net::HTTP::Post.new(uri)
      request["Authorization"] = "Bearer #{source_access_token}"
      request["Content-Type"] = "application/json"
      request.body = JSON.generate(scope: Array(authorization.scope))

      response = Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == "https") do |http|
        http.request(request)
      end
      raise "impersonation access token failed (#{response.code})" unless response.code.to_i.between?(200, 299)

      body = JSON.parse(response.body.presence || "{}")
      @impersonated_access_token = body.fetch("accessToken")
      @impersonated_access_token_expires_at = Time.iso8601(body.fetch("expireTime"))
      @impersonated_access_token
    end

    def token_fresh?
      @impersonated_access_token &&
        @impersonated_access_token_expires_at &&
        Time.now.utc < (@impersonated_access_token_expires_at - TOKEN_REFRESH_BUFFER_SECONDS)
    end

    def authorization
      @authorization ||= Google::Auth.get_application_default([ scope ])
    end
  end
end
