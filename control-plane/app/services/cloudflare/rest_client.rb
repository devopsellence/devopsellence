# frozen_string_literal: true

require "json"
require "net/http"
require "uri"

module Cloudflare
  class RestClient
    DEFAULT_API_BASE = "https://api.cloudflare.com/client/v4"
    Error = Class.new(StandardError) do
      attr_reader :status_code

      def initialize(status_code:, message:)
        @status_code = status_code.to_i
        super(message)
      end
    end

    def initialize(api_token: Devopsellence::RuntimeConfig.current.cloudflare_api_token,
      account_id: Devopsellence::RuntimeConfig.current.cloudflare_account_id,
      zone_id: Devopsellence::RuntimeConfig.current.cloudflare_zone_id,
      api_base: DEFAULT_API_BASE)
      @api_token = api_token.to_s.strip
      @account_id = account_id.to_s.strip
      @zone_id = zone_id.to_s.strip
      @api_base = api_base.to_s.strip.presence || DEFAULT_API_BASE
    end

    def replace_dns_a_records(hostname:, addresses:, ttl: 60)
      replace_dns_records(hostname:, type: "A", values: Array(addresses).map { |entry| entry.to_s.strip }.reject(&:blank?), ttl:, proxied: false)
    end

    def replace_dns_txt_records(hostname:, values:, ttl: 60)
      replace_dns_records(hostname:, type: "TXT", values: Array(values).map { |entry| entry.to_s.strip }.reject(&:blank?), ttl:, proxied: false)
    end

    def delete_dns_records(hostname:, type: nil)
      dns_records(hostname:, type:).each do |record|
        delete_dns_record(record.fetch("id"))
      end
    end

    def dns_records(hostname:, type: nil)
      params = { name: hostname.to_s.strip }
      params[:type] = type.to_s.strip if type.present?
      request(:get, "/zones/#{zone_id}/dns_records?#{URI.encode_www_form(params)}")
    end

    def restore_dns_records(records)
      Array(records).each do |record|
        create_dns_record(
          hostname: record.fetch("name"),
          type: record.fetch("type"),
          content: record.fetch("content"),
          proxied: record.fetch("proxied", false),
          ttl: record["ttl"]
        )
      end
    end

    private

    attr_reader :api_token, :account_id, :zone_id, :api_base

    def request(method, path, payload: nil)
      raise "missing CLOUDFLARE_API_TOKEN" if api_token.blank?
      raise "missing CLOUDFLARE_ACCOUNT_ID" if account_id.blank?
      raise "missing CLOUDFLARE_ZONE_ID" if zone_id.blank?

      uri = URI.join("#{api_base}/", path.delete_prefix("/"))
      request = request_class(method).new(uri)
      request["Authorization"] = "Bearer #{api_token}"
      request["Accept"] = "application/json"
      if payload
        request["Content-Type"] = "application/json"
        request.body = JSON.generate(payload)
      end

      response = Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == "https") do |http|
        http.request(request)
      end
      body = JSON.parse(response.body.presence || "{}")
      status_code = response.code.to_i
      return body.fetch("result") if status_code.between?(200, 299) && body["success"] != false

      errors = Array(body["errors"]).map { |entry| entry.is_a?(Hash) ? entry["message"] : entry.to_s }.reject(&:blank?)
      raise Error.new(
        status_code: status_code,
        message: "cloudflare request failed (#{response.code}): #{errors.presence&.join(", ") || response.body}"
      )
    end

    def request_class(method)
      case method
      when :get then Net::HTTP::Get
      when :post then Net::HTTP::Post
      when :put then Net::HTTP::Put
      when :delete then Net::HTTP::Delete
      else
        raise ArgumentError, "unsupported method: #{method}"
      end
    end

    def replace_dns_records(hostname:, type:, values:, ttl:, proxied:)
      existing = dns_records(hostname:, type:)
      existing.each do |record|
        delete_dns_record(record.fetch("id"))
      end

      values.each do |value|
        create_dns_record(
          hostname: hostname,
          type: type,
          content: value,
          proxied: proxied,
          ttl: ttl
        )
      end
    end

    def create_dns_record(hostname:, type:, content:, proxied:, ttl: nil)
      payload = {
        type: type,
        name: dns_record_name(hostname),
        content: content,
        proxied: proxied
      }
      payload[:ttl] = ttl if ttl

      request(:post, "/zones/#{zone_id}/dns_records", payload: payload)
    rescue Error => error
      raise unless hostname_conflict_error?(error)

      existing = dns_records(hostname:, type:).find do |record|
        dns_record_matches?(record, type:, content:, proxied:)
      end
      return existing if existing

      raise
    end

    def delete_dns_record(record_id)
      request(:delete, "/zones/#{zone_id}/dns_records/#{record_id}")
    rescue Error => error
      raise unless error.status_code == 404
    end

    def hostname_conflict_error?(error)
      error.status_code == 400 && error.message.include?("already exists")
    end

    def dns_record_matches?(record, type:, content:, proxied:)
      record.fetch("type").to_s == type.to_s &&
        record.fetch("content").to_s == content.to_s &&
        (!!record["proxied"] == !!proxied)
    end

    def dns_record_name(hostname)
      zone_hostname = hostname.to_s.strip
      suffix = ".#{zone_name}"
      zone_hostname.end_with?(suffix) ? zone_hostname.delete_suffix(suffix) : zone_hostname
    end

    def zone_name
      Devopsellence::RuntimeConfig.current.cloudflare_zone_name
    end
  end
end
