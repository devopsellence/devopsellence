# frozen_string_literal: true

require "json"

module Releases
  class RuntimeAttributes
    InvalidPayload = Class.new(StandardError)

    def initialize(params:)
      @params = params
    end

    def to_h
      attrs = {
        git_sha: required_string(:git_sha),
        image_repository: required_string(:image_repository),
        image_digest: required_string(:image_digest),
        revision: optional_string(:revision),
        release_command: optional_string(:release_command),
        healthcheck_interval_seconds: integer_or_default(:healthcheck_interval_seconds, 5),
        healthcheck_timeout_seconds: integer_or_default(:healthcheck_timeout_seconds, 2)
      }

      structured = {
        web_json: JSON.generate(required_service(:web, allow_healthcheck: true)),
        worker_json: parse_service(params[:worker], field: :worker, allow_healthcheck: false)&.then { JSON.generate(_1) }
      }
      attrs.merge(structured.compact)
    end

    private

    attr_reader :params

    def required_string(key)
      value = params[key].to_s.strip
      raise InvalidPayload, "#{key} is required" if value.blank?

      value
    end

    def optional_string(key)
      params[key].to_s.strip.presence
    end

    def optional_integer(key)
      value = params[key]
      return nil if value.blank?

      Integer(value)
    rescue ArgumentError, TypeError
      raise InvalidPayload, "#{key} must be an integer"
    end

    def integer_or_default(key, default)
      value = params[key]
      return default if value.blank?

      Integer(value)
    rescue ArgumentError, TypeError
      raise InvalidPayload, "#{key} must be an integer"
    end

    def parse_hash(value, field:)
      case value
      when ActionController::Parameters
        value.to_unsafe_h
      when Hash
        value
      when nil
        {}
      else
        parsed = parse_json(value, field: field, default: {})
        raise InvalidPayload, "#{field} must be a JSON object" unless parsed.is_a?(Hash)

        parsed
      end
    end

    def parse_array(value, field:)
      case value
      when Array
        value.map do |entry|
          entry.is_a?(ActionController::Parameters) ? entry.to_unsafe_h : entry
        end
      when nil
        []
      else
        parsed = parse_json(value, field: field, default: [])
        raise InvalidPayload, "#{field} must be a JSON array" unless parsed.is_a?(Array)

        parsed
      end
    end

    def parse_service(value, field:, allow_healthcheck:)
      return nil if value.blank?

      service = parse_hash(value, field: field)
      if service.key?("healthcheck_path") || service.key?(:healthcheck_path) || service.key?("healthcheck_port") || service.key?(:healthcheck_port)
        raise InvalidPayload, "#{field} must use healthcheck.path and healthcheck.port"
      end
      normalized = {
        "entrypoint" => optional_service_string(service["entrypoint"] || service[:entrypoint]),
        "command" => optional_service_string(service["command"] || service[:command]),
        "env" => parse_hash(service["env"] || service[:env], field: :"#{field}.env"),
        "secret_refs" => parse_array(service["secret_refs"] || service[:secret_refs], field: :"#{field}.secret_refs"),
        "volumes" => parse_array(service["volumes"] || service[:volumes], field: :"#{field}.volumes")
      }

      if allow_healthcheck
        normalized["port"] = optional_service_integer(service["port"] || service[:port], field: :"#{field}.port")
        healthcheck = service["healthcheck"] || service[:healthcheck]
        healthcheck = parse_hash(healthcheck, field: :"#{field}.healthcheck") if healthcheck.present?
        normalized["healthcheck"] = {
          "path" => optional_service_string(healthcheck&.[]("path") || healthcheck&.[](:path)),
          "port" => optional_service_integer(healthcheck&.[]("port") || healthcheck&.[](:port), field: :"#{field}.healthcheck.port")
        }.compact
      elsif service.key?("port") || service.key?(:port) || service.key?("healthcheck") || service.key?(:healthcheck)
        raise InvalidPayload, "#{field} cannot define port or healthcheck"
      end

      normalized.compact
    end

    def required_service(field, allow_healthcheck:)
      service = parse_service(params[field], field:, allow_healthcheck:)
      raise InvalidPayload, "#{field} is required" if service.blank?

      service
    end

    def optional_service_string(value)
      value.to_s.strip.presence
    end

    def optional_service_integer(value, field:)
      return nil if value.blank?

      Integer(value)
    rescue ArgumentError, TypeError
      raise InvalidPayload, "#{field} must be an integer"
    end

    def parse_json(value, field:, default:)
      text = value.to_s.strip
      return default if text.blank?

      JSON.parse(text)
    rescue JSON::ParserError => error
      raise InvalidPayload, "#{field} is invalid JSON: #{error.message}"
    end
  end
end
