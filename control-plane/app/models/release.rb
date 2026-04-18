# frozen_string_literal: true

require "json"
require "shellwords"

class Release < ApplicationRecord
  STATUS_DRAFT = "draft"
  STATUS_PUBLISHED = "published"
  STATUSES = [ STATUS_DRAFT, STATUS_PUBLISHED ].freeze

  belongs_to :project

  has_many :deployments, dependent: :restrict_with_error

  validates :git_sha, presence: true
  validates :image_digest, presence: true
  validates :image_repository, presence: true
  validates :status, inclusion: { in: STATUSES }
  validates :healthcheck_interval_seconds, numericality: { greater_than: 0 }, allow_nil: true
  validates :healthcheck_timeout_seconds, numericality: { greater_than: 0 }, allow_nil: true
  validate :web_json_is_object
  validate :worker_json_is_object
  validate :service_configs_are_valid
  validate :release_command_is_valid

  before_validation :normalize_revision

  def image_reference_for(organization)
    return "#{image_repository}@#{image_digest}" if external_image_reference?

    "#{organization.gar_repository_path}/#{image_repository}@#{image_digest}"
  end

  def external_image_reference?
    repository = image_repository.to_s.strip
    return false if repository.blank?

    registry = repository.split("/").first.to_s
    registry.include?(".") || registry.include?(":") || registry == "localhost"
  end

  def web_service
    parse_service_config(web_json, fallback: {})
  end

  def worker_service
    service = parse_service_config(worker_json, fallback: {})
    service.present? ? service : nil
  end

  def release_command_text
    release_command.to_s.strip.presence
  end

  def has_worker?
    worker_service.present?
  end

  def stateful?
    [web_service, worker_service].compact.any? do |service|
      Array(service["volumes"]).any?
    end
  end

  def has_release_command?
    release_command_text.present?
  end

  def scheduled_services_for(node:)
    environment = node.environment
    services = []
    services << service_payload("web", "web", web_service, organization: node.organization, environment: environment) if node.labeled?(Node::LABEL_WEB) && web_service.present?
    services << service_payload("worker", "worker", worker_service, organization: node.organization, environment: environment) if node.labeled?(Node::LABEL_WORKER) && worker_service.present?
    services.compact
  end

  def release_command_task_for(node:)
    return nil unless release_command_text.present?
    return nil unless web_service.present?

    task_payload(
      "release_command",
      web_service.merge("command" => release_command_text),
      organization: node.organization,
      environment: node.environment,
      secret_service_name: "web"
    )
  end

  def requires_label?(label)
    case label.to_s
    when Node::LABEL_WEB
      web_service.present?
    when Node::LABEL_WORKER
      worker_service.present?
    else
      false
    end
  end

  private

  def normalize_revision
    self.revision = git_sha if revision.blank? && git_sha.present?
  end

  def web_json_is_object
    parse_service_config(web_json, fallback: {})
  rescue JSON::ParserError
    errors.add(:web_json, "must be valid JSON")
  rescue TypeError
    errors.add(:web_json, "must decode to an object")
  end

  def worker_json_is_object
    parse_service_config(worker_json, fallback: {})
  rescue JSON::ParserError
    errors.add(:worker_json, "must be valid JSON")
  rescue TypeError
    errors.add(:worker_json, "must decode to an object")
  end

  def service_configs_are_valid
    validate_service_config("web", web_service, allow_healthcheck: true)
    validate_service_config("worker", worker_service, allow_healthcheck: false)
  end

  def release_command_is_valid
    return if release_command.nil?
    return if release_command_text.present?

    errors.add(:release_command, "must be a non-empty string")
  end

  def validate_service_config(name, service, allow_healthcheck:, invalid_service_message: nil)
    return if service.blank?

    unless service.is_a?(Hash)
      errors.add(:"#{name}_json", "must decode to an object")
      return
    end

    if service["entrypoint"].present? && !service["entrypoint"].is_a?(String)
      errors.add(:"#{name}_json", "entrypoint must be a string")
    end
    if service["command"].present? && !service["command"].is_a?(String)
      errors.add(:"#{name}_json", "command must be a string")
    end

    unless service["env"].nil? || service["env"].is_a?(Hash)
      errors.add(:"#{name}_json", "env must be an object")
    end

    unless service["secret_refs"].nil? || service["secret_refs"].is_a?(Array)
      errors.add(:"#{name}_json", "secret_refs must be an array")
    end

    unless service["volumes"].nil? || service["volumes"].is_a?(Array)
      errors.add(:"#{name}_json", "volumes must be an array")
    end

    if service.key?("healthcheck_path") || service.key?("healthcheck_port")
      errors.add(:"#{name}_json", "must use healthcheck.path and healthcheck.port")
      return
    end

    Array(service["secret_refs"]).each do |entry|
      unless entry.is_a?(Hash) && entry["name"].to_s.strip.present? && entry["secret"].to_s.strip.present?
        errors.add(:"#{name}_json", "secret_refs entries must include name and secret")
        break
      end
    end

    Array(service["volumes"]).each do |entry|
      unless entry.is_a?(Hash) && entry["source"].to_s.strip.present? && entry["target"].to_s.strip.start_with?("/")
        errors.add(:"#{name}_json", "volumes entries must include source and absolute target")
        break
      end
    end

    if allow_healthcheck
      port = service["port"]
      if !port.is_a?(Integer) || port <= 0
        errors.add(:"#{name}_json", "port must be a positive integer")
      end

      healthcheck = service["healthcheck"]
      unless healthcheck.is_a?(Hash)
        errors.add(:"#{name}_json", "healthcheck must be an object")
        return
      end

      path = healthcheck["path"]
      if !path.is_a?(String) || path.strip.empty?
        errors.add(:"#{name}_json", "healthcheck.path must be present")
      end

      healthcheck_port = healthcheck["port"]
      if !healthcheck_port.nil? && (!healthcheck_port.is_a?(Integer) || healthcheck_port <= 0)
        errors.add(:"#{name}_json", "healthcheck.port must be a positive integer")
      end
    elsif service.key?("port") || service.key?("healthcheck")
      errors.add(:"#{name}_json", invalid_service_message || "#{name} cannot define port or healthcheck")
    end
  end

  def service_payload(name, kind, service, organization:, environment:)
    return nil if service.blank?

    payload = {
      name: name,
      kind: kind,
      image: image_reference_for(organization),
      entrypoint: shell_words(service["entrypoint"]),
      command: shell_words(service["command"]),
      env: service["env"].presence,
      secretRefs: merged_secret_refs_for_agent(service, service_name: name, environment: environment).presence,
      volumeMounts: service_volume_mounts(service, environment: environment).presence
    }.compact

    if kind == "web"
      payload[:ports] = [
        {
          name: "http",
          port: service_port_for(service)
        }
      ]
      payload[:healthcheck] = service_healthcheck(service)
    end

    payload
  end

  def task_payload(name, service, organization:, environment:, secret_service_name: nil)
    return nil if service.blank?

    {
      name: name,
      image: image_reference_for(organization),
      entrypoint: shell_words(service["entrypoint"]),
      command: shell_words(service["command"]),
      env: service["env"].presence,
      secretRefs: resolved_task_secret_refs(service, secret_service_name:, environment:).presence,
      volumeMounts: service_volume_mounts(service, environment: environment).presence
    }.compact
  end

  def service_healthcheck(service)
    healthcheck = service["healthcheck"]
    return nil unless healthcheck.is_a?(Hash)

    path = healthcheck["path"].presence
    return nil if path.blank?

    {
      path: path,
      port: service_healthcheck_port_for(service),
      intervalSeconds: healthcheck_interval_seconds,
      timeoutSeconds: healthcheck_timeout_seconds,
      retries: 3,
      startPeriodSeconds: 1
    }
  end

  def service_port_for(service)
    service["port"] || 3000
  end

  def service_healthcheck_port_for(service)
    healthcheck = service["healthcheck"]
    return service_port_for(service) unless healthcheck.is_a?(Hash)

    healthcheck["port"] || service_port_for(service)
  end

  def shell_words(value)
    text = value.to_s.strip
    return nil if text.empty?

    Shellwords.split(text)
  end

  def parse_service_config(value, fallback:)
    parsed = parse_json_object(value, fallback)
    stringify_service_config(parsed)
  end

  def stringify_service_config(value)
    value.each_with_object({}) do |(key, entry), result|
      result[key.to_s] =
        case entry
        when Hash
          stringify_service_config(entry)
        when Array
          entry.map { |item| item.is_a?(Hash) ? stringify_service_config(item) : item }
        else
          entry
        end
    end
  end

  def service_secret_refs_for_agent(service)
    Array(service["secret_refs"]).each_with_object({}) do |entry, result|
      next unless entry.is_a?(Hash)

      name = entry["name"].presence || entry[:name].presence
      secret = entry["secret"].presence || entry[:secret].presence
      next if name.blank? || secret.blank?

      result[name] = secret
    end
  end

  def merged_secret_refs_for_agent(service, service_name:, environment:)
    service_secret_refs_for_agent(service).merge(environment&.managed_secret_refs_for(service_name) || {})
  end

  def resolved_task_secret_refs(service, secret_service_name:, environment:)
    return service_secret_refs_for_agent(service) unless secret_service_name.present?

    merged_secret_refs_for_agent(service, service_name: secret_service_name, environment: environment)
  end

  def service_volume_mounts(service, environment:)
    Array(service["volumes"]).filter_map do |entry|
      next unless entry.is_a?(Hash)

      source = entry["source"].to_s.strip
      target = entry["target"].to_s.strip
      next if source.empty? || target.empty?

      { source: environment_scoped_volume_name(source, environment: environment), target: target }
    end
  end

  def environment_scoped_volume_name(source, environment:)
    "devopsellence-env-#{environment.id}-#{source}"
  end

  def parse_json_object(value, fallback = nil)
    parsed = JSON.parse(value.presence || "{}")
    raise TypeError unless parsed.is_a?(Hash)

    parsed
  rescue JSON::ParserError, TypeError
    return fallback unless fallback.nil?

    raise
  end

end
