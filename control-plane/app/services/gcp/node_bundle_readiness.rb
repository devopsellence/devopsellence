# frozen_string_literal: true

require "erb"
require "json"
require "net/http"
require "uri"

module Gcp
  class NodeBundleReadiness
    RETRY_DELAYS = [ 1, 2, 4, 8, 16, 32, 64 ].freeze
    SCOPE = "https://www.googleapis.com/auth/cloud-platform"
    Result = Struct.new(:status, :message, keyword_init: true)

    def initialize(node_bundle:, issuer:, client: nil, retry_delays: RETRY_DELAYS, sleeper: nil, subject_token_issuer: Idp::SubjectTokenIssuer)
      @node_bundle = node_bundle
      @environment_bundle = node_bundle.environment_bundle
      @issuer = issuer
      @client = client || HttpClient.new
      @retry_delays = Array(retry_delays)
      @sleeper = sleeper || ->(seconds) { sleep(seconds) if seconds.to_f.positive? }
      @subject_token_issuer = subject_token_issuer
    end

    def call
      attempt = 0

      loop do
        attempt += 1

        begin
          federated_token = exchange_subject_token!
          verify_impersonation!(federated_token)
          return Result.new(status: :ready, message: nil)
        rescue ProbeError => error
          return Result.new(status: :failed, message: utf8(error.message)) unless retryable?(error, attempt)

          sleeper.call(retry_delays.fetch(attempt - 1, 0))
        rescue StandardError => error
          return Result.new(status: :failed, message: utf8(error.message))
        end
      end
    end

    private

    attr_reader :node_bundle, :environment_bundle, :issuer, :client, :retry_delays, :sleeper, :subject_token_issuer

    def exchange_subject_token!
      response = client.post(
        Gcp::Endpoints.sts_token_url,
        payload: {
          grantType: "urn:ietf:params:oauth:grant-type:token-exchange",
          requestedTokenType: "urn:ietf:params:oauth:token-type:access_token",
          subjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
          subjectToken: subject_token_issuer.issue_for_bundle!(node_bundle:, issuer:),
          audience: node_bundle.runtime_project.audience,
          scope: SCOPE
        }
      )

      body = parse_json(response)
      token = body["access_token"].to_s.strip
      return token if success?(response) && token.present?

      raise probe_error("sts", response.code.to_i, response.body)
    end

    def verify_impersonation!(federated_token)
      response = client.post(
        "#{Gcp::Endpoints.iam_credentials_base}/projects/-/serviceAccounts/#{ERB::Util.url_encode(environment_bundle.service_account_email)}:generateAccessToken",
        payload: { scope: [ SCOPE ] },
        headers: { "Authorization" => "Bearer #{federated_token}" }
      )

      body = parse_json(response)
      access_token = body["accessToken"].to_s.strip
      return true if success?(response) && access_token.present?

      raise probe_error("iamcredentials", response.code.to_i, response.body)
    end

    def parse_json(response)
      JSON.parse(response.body.to_s)
    rescue JSON::ParserError
      {}
    end

    def success?(response)
      response.code.to_i.between?(200, 299)
    end

    def retryable?(error, attempt)
      return false unless error.retryable?

      attempt <= retry_delays.length
    end

    def probe_error(source, status_code, body)
      sanitized = utf8(body)
      retryable = retryable_status?(source, status_code, sanitized)
      ProbeError.new("#{source} readiness check failed (#{status_code}): #{sanitized}", retryable:)
    end

    def retryable_status?(source, status_code, body)
      return true if status_code >= 500
      return false unless source == "iamcredentials"
      return true if status_code == 404

      status_code == 403 && body.match?(/iam_permission_denied|iam\.serviceAccounts\.getAccessToken|permission.*getAccessToken/i)
    end

    def utf8(value)
      value.to_s.encode("UTF-8", invalid: :replace, undef: :replace, replace: "?")
    end

    class ProbeError < StandardError
      attr_reader :retryable

      def initialize(message, retryable:)
        super(message)
        @retryable = retryable
      end

      def retryable?
        retryable
      end
    end

    class HttpClient
      def post(uri, payload:, headers: {})
        parsed = URI.parse(uri)
        request = Net::HTTP::Post.new(parsed)
        request["Accept"] = "application/json"
        request["Content-Type"] = "application/json"
        headers.each { |key, value| request[key] = value }
        request.body = JSON.generate(payload)

        Net::HTTP.start(parsed.host, parsed.port, use_ssl: parsed.scheme == "https") do |http|
          http.request(request)
        end
      end
    end
  end
end
