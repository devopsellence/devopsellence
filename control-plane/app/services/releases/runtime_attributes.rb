# frozen_string_literal: true

require "json"

module Releases
  class RuntimeAttributes
    InvalidPayload = Class.new(StandardError)
    SERVICE_KINDS = [ "web", "worker", "accessory" ].freeze

    def initialize(params:)
      @params = params
    end

    def to_h
      attrs = {
        git_sha: required_string(:git_sha),
        image_repository: required_string(:image_repository),
        image_digest: required_string(:image_digest),
        revision: optional_string(:revision),
        healthcheck_interval_seconds: integer_or_default(:healthcheck_interval_seconds, 5),
        healthcheck_timeout_seconds: integer_or_default(:healthcheck_timeout_seconds, 2)
      }

      runtime = {
        "services" => parse_services(params[:services]),
        "tasks" => parse_tasks(params[:tasks]),
        "ingress" => parse_ingress(params[:ingress])
      }.compact

      attrs.merge(runtime_json: JSON.generate(runtime))
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

    def integer_or_default(key, default)
      value = params[key]
      return default if value.blank?

      Integer(value)
    rescue ArgumentError, TypeError
      raise InvalidPayload, "#{key} must be an integer"
    end

    def parse_services(value)
      services = parse_hash(value, field: :services)
      raise InvalidPayload, "services is required" if services.blank?

      services.each_with_object({}) do |(name, raw), result|
        service_name = name.to_s.strip
        raise InvalidPayload, "services keys must be present" if service_name.blank?

        result[service_name] = parse_service(raw, field: :"services.#{service_name}")
      end
    end

    def parse_tasks(value)
      tasks = parse_hash(value, field: :tasks)
      return {} if tasks.blank?

      parsed = {}
      if tasks.key?("release") || tasks.key?(:release)
        parsed["release"] = parse_release_task(tasks["release"] || tasks[:release])
      end
      parsed
    end

    def parse_service(value, field:)
      service = parse_hash(value, field:)
      reject_deprecated_key!(service, deprecated_key: :entrypoint, field: :"#{field}.entrypoint")
      kind = optional_service_string(service["kind"] || service[:kind])
      raise InvalidPayload, "#{field}.kind must be present" if kind.blank?

      unless SERVICE_KINDS.include?(kind)
        raise InvalidPayload, "#{field}.kind must be one of #{SERVICE_KINDS.join(', ')}"
      end

      normalized = {
        "kind" => kind,
        "image" => optional_service_string(service["image"] || service[:image]),
        "command" => optional_service_array(service["command"] || service[:command], field: :"#{field}.command"),
        "args" => optional_service_array(service["args"] || service[:args], field: :"#{field}.args"),
        "env" => parse_hash(service["env"] || service[:env], field: :"#{field}.env"),
        "secret_refs" => parse_array(service["secret_refs"] || service[:secret_refs], field: :"#{field}.secret_refs"),
        "volumes" => parse_array(service["volumes"] || service[:volumes], field: :"#{field}.volumes"),
        "ports" => parse_ports(service["ports"] || service[:ports], field: :"#{field}.ports")
      }.compact

      healthcheck = service["healthcheck"] || service[:healthcheck]
      if healthcheck.present?
        healthcheck = parse_hash(healthcheck, field: :"#{field}.healthcheck")
        normalized["healthcheck"] = {
          "path" => optional_service_string(healthcheck["path"] || healthcheck[:path]),
          "port" => optional_service_integer(healthcheck["port"] || healthcheck[:port], field: :"#{field}.healthcheck.port")
        }.compact
      end

      normalized
    end

    def parse_release_task(value)
      task = parse_hash(value, field: :"tasks.release")
      reject_deprecated_key!(task, deprecated_key: :entrypoint, field: :"tasks.release.entrypoint")
      {
        "service" => required_service_string(task["service"] || task[:service], field: :"tasks.release.service"),
        "command" => optional_service_array(task["command"] || task[:command], field: :"tasks.release.command"),
        "args" => optional_service_array(task["args"] || task[:args], field: :"tasks.release.args"),
        "env" => parse_hash(task["env"] || task[:env], field: :"tasks.release.env")
      }.compact
    end

    def parse_ingress(value)
      ingress = parse_hash(value, field: :ingress)
      return nil if ingress.blank?

      redirect_http =
        if ingress.key?("redirect_http")
          ingress["redirect_http"]
        elsif ingress.key?(:redirect_http)
          ingress[:redirect_http]
        end

      tls = ingress["tls"] || ingress[:tls]
      parsed = {
        "hosts" => optional_service_array(ingress["hosts"] || ingress[:hosts], field: :"ingress.hosts"),
        "service" => optional_service_string(ingress["service"] || ingress[:service]),
        "redirect_http" => optional_boolean(redirect_http, field: :"ingress.redirect_http")
      }.compact
      if tls.present?
        tls = parse_hash(tls, field: :"ingress.tls")
        parsed["tls"] = {
          "mode" => optional_service_string(tls["mode"] || tls[:mode]),
          "email" => optional_service_string(tls["email"] || tls[:email]),
          "ca_directory_url" => optional_service_string(tls["ca_directory_url"] || tls[:ca_directory_url])
        }.compact
      end
      parsed
    end

    def parse_ports(value, field:)
      parse_array(value, field:).map.with_index do |entry, index|
        port = parse_hash(entry, field: :"#{field}[#{index}]")
        {
          "name" => required_service_string(port["name"] || port[:name], field: :"#{field}[#{index}].name"),
          "port" => required_service_integer(port["port"] || port[:port], field: :"#{field}[#{index}].port")
        }
      end
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
        value.map { |entry| entry.is_a?(ActionController::Parameters) ? entry.to_unsafe_h : entry }
      when ActionController::Parameters
        value.to_unsafe_h.sort_by { |key, _entry| key.to_s }.map(&:second)
      when Hash
        value.sort_by { |key, _entry| key.to_s }.map(&:second)
      when nil
        []
      else
        parsed = parse_json(value, field: field, default: [])
        raise InvalidPayload, "#{field} must be a JSON array" unless parsed.is_a?(Array)

        parsed
      end
    end

    def parse_json(value, field:, default:)
      return default if value.blank?

      JSON.parse(value)
    rescue JSON::ParserError, TypeError
      raise InvalidPayload, "#{field} must be valid JSON"
    end

    def optional_service_string(value)
      value.to_s.strip.presence
    end

    def optional_service_array(value, field:)
      array = parse_array(value, field: field)
      array.each_with_index.map do |entry, index|
        raise InvalidPayload, "#{field}[#{index}] must be a string" unless entry.is_a?(String)

        text = entry.strip
        raise InvalidPayload, "#{field}[#{index}] must be present" if text.blank?

        text
      end.presence
    end

    def reject_deprecated_key!(hash, deprecated_key:, field:)
      return unless hash.key?(deprecated_key) || hash.key?(deprecated_key.to_s)

      raise InvalidPayload, "#{field} is no longer supported; use command or args"
    end

    def required_service_string(value, field:)
      text = value.to_s.strip
      raise InvalidPayload, "#{field} is required" if text.blank?

      text
    end

    def optional_service_integer(value, field:)
      return nil if value.blank?

      Integer(value)
    rescue ArgumentError, TypeError
      raise InvalidPayload, "#{field} must be an integer"
    end

    def required_service_integer(value, field:)
      integer = optional_service_integer(value, field:)
      raise InvalidPayload, "#{field} is required" if integer.nil?

      integer
    end

    def optional_boolean(value, field:)
      return nil if value.nil? || value == ""
      return value if value == true || value == false

      raise InvalidPayload, "#{field} must be a boolean"
    end
  end
end
