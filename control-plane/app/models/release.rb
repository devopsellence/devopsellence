# frozen_string_literal: true

require "json"
require "shellwords"

class Release < ApplicationRecord
  STATUS_DRAFT = "draft"
  STATUS_PUBLISHED = "published"
  STATUSES = [ STATUS_DRAFT, STATUS_PUBLISHED ].freeze
  SERVICE_KINDS = [ "web", "worker", "accessory" ].freeze

  belongs_to :project

  has_many :deployments, dependent: :restrict_with_error

  validates :git_sha, presence: true
  validates :image_digest, presence: true
  validates :image_repository, presence: true
  validates :status, inclusion: { in: STATUSES }
  validates :healthcheck_interval_seconds, numericality: { greater_than: 0 }, allow_nil: true
  validates :healthcheck_timeout_seconds, numericality: { greater_than: 0 }, allow_nil: true
  validate :runtime_json_is_object
  validate :runtime_configs_are_valid

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

  def runtime_payload
    payload = parse_json_object(runtime_json, fallback: default_runtime_payload)
    stringify_hash(payload)
  end

  def services_config
    stringify_hash(runtime_payload["services"])
  end

  def tasks_config
    stringify_hash(runtime_payload["tasks"])
  end

  def service_names
    services_config.keys.sort
  end

  def web_service_names
    service_names.select { |name| service_kind(services_config[name]) == "web" }
  end

  def release_task_config
    task = tasks_config["release"]
    task.is_a?(Hash) ? task : nil
  end

  def has_release_task?
    release_task_config.present?
  end

  def stateful?
    services_config.values.any? { |service| Array(service["volumes"]).any? }
  end

  def required_roles
    services_config.values.flat_map { |service| service_roles(service) }.uniq.sort
  end

  def requires_role?(label)
    required_roles.include?(label.to_s.strip)
  end

  def service_roles_for(name)
    service = services_config[name.to_s]
    return [] if service.blank?

    service_roles(service)
  end

  def service_scheduled_on?(service_name, node)
    node.labeled_any?(service_roles_for(service_name))
  end

  def ingress_service_name
    configured = runtime_payload["ingress_service"].to_s.strip
    return configured if configured.present?

    return "web" if services_config["web"] && service_kind(services_config["web"]) == "web"

    web_service_names.first
  end

  def scheduled_services_for(node:)
    environment = node.environment
    service_names.filter_map do |name|
      service = services_config[name]
      next unless node.labeled_any?(service_roles(service))

      service_payload(name, service, organization: node.organization, environment: environment)
    end
  end

  def release_task_for(node:)
    task = release_task_config
    return nil unless task

    service_name = task["service"].to_s.strip
    service = services_config[service_name]
    return nil if service.blank?
    return nil unless node.labeled_any?(service_roles(service))

    task_payload(
      "release",
      merged_task_service(service, task),
      organization: node.organization,
      environment: node.environment,
      secret_service_name: service_name
    )
  end

  def release_task_service_name
    release_task_config&.dig("service").to_s.strip.presence
  end

  private

  def normalize_revision
    self.revision = git_sha if revision.blank? && git_sha.present?
  end

  def runtime_json_is_object
    parse_json_object(runtime_json)
  rescue JSON::ParserError
    errors.add(:runtime_json, "must be valid JSON")
  rescue TypeError
    errors.add(:runtime_json, "must decode to an object")
  end

  def runtime_configs_are_valid
    services = services_config
    if services.blank?
      errors.add(:runtime_json, "must include at least one service")
      return
    end

    services.each do |name, service|
      validate_service_config(name, service)
    end

    if web_service_names.empty?
      errors.add(:runtime_json, "must include at least one web service")
    end

    validate_release_task
    validate_ingress_service
  end

  def validate_service_config(name, service)
    unless service.is_a?(Hash)
      errors.add(:runtime_json, "services.#{name} must decode to an object")
      return
    end

    kind = service_kind(service)
    unless SERVICE_KINDS.include?(kind)
      errors.add(:runtime_json, "services.#{name}.kind must be one of #{SERVICE_KINDS.join(', ')}")
    end

    roles = service_roles(service)
    if roles.empty?
      errors.add(:runtime_json, "services.#{name}.roles must include at least one role")
    end

    if service["entrypoint"].present? && !service["entrypoint"].is_a?(String)
      errors.add(:runtime_json, "services.#{name}.entrypoint must be a string")
    end
    if service["command"].present? && !service["command"].is_a?(String)
      errors.add(:runtime_json, "services.#{name}.command must be a string")
    end
    unless service["env"].nil? || service["env"].is_a?(Hash)
      errors.add(:runtime_json, "services.#{name}.env must be an object")
    end
    unless service["secret_refs"].nil? || service["secret_refs"].is_a?(Array)
      errors.add(:runtime_json, "services.#{name}.secret_refs must be an array")
    end
    unless service["volumes"].nil? || service["volumes"].is_a?(Array)
      errors.add(:runtime_json, "services.#{name}.volumes must be an array")
    end
    unless service["ports"].nil? || service["ports"].is_a?(Array)
      errors.add(:runtime_json, "services.#{name}.ports must be an array")
    end

    Array(service["secret_refs"]).each do |entry|
      unless entry.is_a?(Hash) && entry["name"].to_s.strip.present? && entry["secret"].to_s.strip.present?
        errors.add(:runtime_json, "services.#{name}.secret_refs entries must include name and secret")
        break
      end
    end

    Array(service["volumes"]).each do |entry|
      unless entry.is_a?(Hash) && entry["source"].to_s.strip.present? && entry["target"].to_s.strip.start_with?("/")
        errors.add(:runtime_json, "services.#{name}.volumes entries must include source and absolute target")
        break
      end
    end

    ports = service_ports(service)
    if kind == "web" && http_port(service).to_i <= 0
      errors.add(:runtime_json, "services.#{name} must expose an http port")
    end
    if ports.map { |port| port["name"] }.uniq.length != ports.length
      errors.add(:runtime_json, "services.#{name}.ports must have unique names")
    end
    ports.each do |port|
      unless port["name"].to_s.strip.present? && port["port"].is_a?(Integer) && port["port"].positive?
        errors.add(:runtime_json, "services.#{name}.ports entries must include name and positive port")
        break
      end
    end

    healthcheck = service["healthcheck"]
    if kind == "web"
      unless healthcheck.is_a?(Hash)
        errors.add(:runtime_json, "services.#{name}.healthcheck must be an object")
        return
      end
      unless healthcheck["path"].to_s.strip.present?
        errors.add(:runtime_json, "services.#{name}.healthcheck.path must be present")
      end
      unless healthcheck["port"].is_a?(Integer) && healthcheck["port"].positive?
        errors.add(:runtime_json, "services.#{name}.healthcheck.port must be a positive integer")
      end
    end
  end

  def validate_release_task
    task = release_task_config
    return if task.nil?

    service_name = task["service"].to_s.strip
    if service_name.blank?
      errors.add(:runtime_json, "tasks.release.service is required")
      return
    end
    service = services_config[service_name]
    if service.blank?
      errors.add(:runtime_json, "tasks.release.service must reference an existing service")
      return
    end
    if task["entrypoint"].to_s.strip.blank? && task["command"].to_s.strip.blank?
      errors.add(:runtime_json, "tasks.release must set entrypoint or command")
    end
    unless task["env"].nil? || task["env"].is_a?(Hash)
      errors.add(:runtime_json, "tasks.release.env must be an object")
    end
    if service_roles(service).empty?
      errors.add(:runtime_json, "tasks.release.service must reference a service with roles")
    end
  end

  def validate_ingress_service
    name = ingress_service_name
    return if name.blank?

    service = services_config[name]
    if service.blank?
      errors.add(:runtime_json, "ingress_service must reference an existing service")
      return
    end
    if service_kind(service) != "web"
      errors.add(:runtime_json, "ingress_service must reference a web service")
    end
  end

  def service_payload(name, service, organization:, environment:)
    image = service["image"].to_s.strip.presence || image_reference_for(organization)
    payload = {
      name: name,
      kind: service_kind(service),
      image: image,
      entrypoint: shell_words(service["entrypoint"]),
      command: shell_words(service["command"]),
      env: service["env"].presence,
      secretRefs: merged_secret_refs_for_agent(service, service_name: name, environment: environment).presence,
      volumeMounts: service_volume_mounts(service, environment: environment).presence
    }.compact

    ports = desired_state_ports(service)
    payload[:ports] = ports if ports.present?
    payload[:healthcheck] = service_healthcheck(service) if service["healthcheck"].present?

    payload
  end

  def task_payload(name, service, organization:, environment:, secret_service_name: nil)
    image = service["image"].to_s.strip.presence || image_reference_for(organization)

    {
      name: name,
      image: image,
      entrypoint: shell_words(service["entrypoint"]),
      command: shell_words(service["command"]),
      env: service["env"].presence,
      secretRefs: resolved_task_secret_refs(service, secret_service_name:, environment:).presence,
      volumeMounts: service_volume_mounts(service, environment: environment).presence
    }.compact
  end

  def merged_task_service(service, task)
    stringify_hash(service).merge(
      "entrypoint" => task["entrypoint"].presence || service["entrypoint"],
      "command" => task["command"].presence || service["command"],
      "env" => stringify_hash(service["env"]).merge(stringify_hash(task["env"]))
    )
  end

  def desired_state_ports(service)
    service_ports(service).map do |port|
      {
        name: port["name"],
        port: port["port"]
      }
    end
  end

  def service_healthcheck(service)
    healthcheck = service["healthcheck"]
    return nil unless healthcheck.is_a?(Hash)

    path = healthcheck["path"].presence
    return nil if path.blank?

    {
      path: path,
      port: healthcheck["port"],
      intervalSeconds: healthcheck_interval_seconds,
      timeoutSeconds: healthcheck_timeout_seconds,
      retries: 3,
      startPeriodSeconds: 1
    }
  end

  def http_port(service)
    service_ports(service).find { |port| port["name"] == "http" }&.dig("port")
  end

  def service_ports(service)
    Array(service["ports"]).filter_map do |entry|
      next unless entry.is_a?(Hash)

      stringify_hash(entry).slice("name", "port")
    end
  end

  def service_kind(service)
    service["kind"].to_s.strip
  end

  def service_roles(service)
    Array(service["roles"]).filter_map { |role| role.to_s.strip.presence }
  end

  def shell_words(value)
    text = value.to_s.strip
    return nil if text.empty?

    Shellwords.split(text)
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

  def parse_json_object(value, fallback: nil)
    parsed = JSON.parse(value.presence || "{}")
    raise TypeError unless parsed.is_a?(Hash)

    parsed
  rescue JSON::ParserError, TypeError
    return fallback unless fallback.nil?

    raise
  end

  def stringify_hash(value)
    return {} unless value.is_a?(Hash)

    value.each_with_object({}) do |(key, entry), result|
      result[key.to_s] =
        case entry
        when Hash
          stringify_hash(entry)
        when Array
          entry.map { |item| item.is_a?(Hash) ? stringify_hash(item) : item }
        else
          entry
        end
    end
  end

  def default_runtime_payload
    { "services" => {}, "tasks" => {} }
  end
end
