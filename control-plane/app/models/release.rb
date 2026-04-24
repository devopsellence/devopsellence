# frozen_string_literal: true

require "json"

class Release < ApplicationRecord
  InvalidRuntimeConfig = Class.new(StandardError)
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

  def ingress_config
    ingress = runtime_payload["ingress"]
    return ingress if ingress.is_a?(Hash)

    legacy_service = runtime_payload["ingress_service"].to_s.strip
    return nil if legacy_service.blank?

    { "service" => legacy_service }
  end

  def has_release_task?
    release_task_config.present?
  end

  def stateful?
    services_config.values.any? { |service| Array(service["volumes"]).any? }
  end

  def required_labels
    services_config.values.filter_map { |service| service_label(service).presence }.uniq.sort
  end

  def requires_label?(label)
    required_labels.include?(label.to_s.strip)
  end

  def service_label_for(name)
    service = services_config[name.to_s]
    return nil if service.blank?

    service_label(service)
  end

  def service_scheduled_on?(service_name, node)
    label = service_label_for(service_name)
    label.present? && node.labeled?(label)
  end

  def ingress_service_name
    ingress_config&.dig("service").to_s.strip.presence
  end

  def scheduled_services_for(node:)
    assert_supported_runtime_payload!

    environment = node.environment
    service_names.filter_map do |name|
      service = services_config[name]
      next unless node.labeled?(service_label(service))

      service_payload(name, service, organization: node.organization, environment: environment)
    end
  end

  def release_task_for(node:)
    assert_supported_runtime_payload!

    task = release_task_config
    return nil unless task

    service_name = task["service"].to_s.strip
    service = services_config[service_name]
    return nil if service.blank?
    return nil unless node.labeled?(service_label(service))

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
    validate_ingress_config
  end

  def validate_service_config(name, service)
    unless service.is_a?(Hash)
      errors.add(:runtime_json, "services.#{name} must decode to an object")
      return
    end

    kind = service_kind(service)
    if kind.blank?
      errors.add(:runtime_json, "services.#{name}.kind must be present")
      return
    end

    unless SERVICE_KINDS.include?(kind)
      errors.add(:runtime_json, "services.#{name}.kind must be one of #{SERVICE_KINDS.join(', ')}")
    end

    validate_string_array(service, name:, field: "command")
    validate_string_array(service, name:, field: "args")
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
    validate_release_task_array(task, "command")
    validate_release_task_array(task, "args")
    if !release_task_array_present?(task["command"]) && !release_task_array_present?(task["args"])
      errors.add(:runtime_json, "tasks.release must set command or args")
    end
    unless task["env"].nil? || task["env"].is_a?(Hash)
      errors.add(:runtime_json, "tasks.release.env must be an object")
    end
    if service_kind(service).blank?
      errors.add(:runtime_json, "tasks.release.service must reference a service with kind")
    end
  end

  def validate_ingress_config
    ingress = ingress_config
    if ingress.nil?
      if web_service_names.length > 1
        errors.add(:runtime_json, "ingress.service is required when multiple web services are defined")
      end
      return
    end

    hosts = ingress["hosts"]
    unless hosts.nil? || string_array?(hosts)
      errors.add(:runtime_json, "ingress.hosts must be an array of strings")
    end
    if string_array?(hosts) && IngressHostnames.normalize_all(hosts).length != hosts.length
      errors.add(:runtime_json, "ingress.hosts must be unique")
    end

    name = ingress["service"].to_s.strip
    if name.blank?
      errors.add(:runtime_json, "ingress.service is required")
      return
    end

    service = services_config[name]
    if service.blank?
      errors.add(:runtime_json, "ingress.service must reference an existing service")
      return
    end
    if service_kind(service) != "web"
      errors.add(:runtime_json, "ingress.service must reference a web service")
    end

    tls = ingress["tls"]
    unless tls.nil? || tls.is_a?(Hash)
      errors.add(:runtime_json, "ingress.tls must be an object")
      return
    end
    if tls.is_a?(Hash)
      mode = tls["mode"].to_s.strip
      if mode.present? && ![ "auto", "off", "manual" ].include?(mode)
        errors.add(:runtime_json, "ingress.tls.mode must be auto, off, or manual")
      end
    end

    redirect_http = ingress["redirect_http"]
    unless redirect_http.nil? || redirect_http == true || redirect_http == false
      errors.add(:runtime_json, "ingress.redirect_http must be a boolean")
    end
  end

  def validate_string_array(service, name:, field:)
    value = service[field]
    return if value.nil? || string_array?(value)

    errors.add(:runtime_json, "services.#{name}.#{field} must be an array of strings")
  end

  def validate_release_task_array(task, field)
    value = task[field]
    return if value.nil? || string_array?(value)

    errors.add(:runtime_json, "tasks.release.#{field} must be an array of strings")
  end

  def release_task_array_present?(value)
    string_array?(value) && value.present?
  end

  def assert_supported_runtime_payload!
    services_config.each do |name, service|
      assert_supported_runtime_service!(name, service)
    end

    if tasks_config.key?("release")
      task = tasks_config["release"]
      raise InvalidRuntimeConfig, "tasks.release must decode to an object" unless task.is_a?(Hash)

      assert_deprecated_runtime_key_absent!(task, deprecated_key: "entrypoint", field: "tasks.release.entrypoint")
      assert_runtime_string_array!(task["command"], field: "tasks.release.command")
      assert_runtime_string_array!(task["args"], field: "tasks.release.args")
    end

    return unless runtime_payload.key?("ingress")

    ingress = runtime_payload["ingress"]
    raise InvalidRuntimeConfig, "ingress must decode to an object" unless ingress.is_a?(Hash)

    assert_runtime_string_array!(ingress["hosts"], field: "ingress.hosts")
  end

  def assert_supported_runtime_service!(name, service)
    raise InvalidRuntimeConfig, "services.#{name} must decode to an object" unless service.is_a?(Hash)

    assert_deprecated_runtime_key_absent!(service, deprecated_key: "entrypoint", field: "services.#{name}.entrypoint")
    assert_runtime_string_array!(service["command"], field: "services.#{name}.command")
    assert_runtime_string_array!(service["args"], field: "services.#{name}.args")
  end

  def assert_deprecated_runtime_key_absent!(hash, deprecated_key:, field:)
    return unless hash.key?(deprecated_key)

    raise InvalidRuntimeConfig, "#{field} is no longer supported; use command or args"
  end

  def assert_runtime_string_array!(value, field:)
    return if value.nil? || string_array?(value)

    raise InvalidRuntimeConfig, "#{field} must be an array of strings"
  end

  def string_array?(value)
    value.is_a?(Array) && value.all? { |entry| entry.is_a?(String) && entry.strip.present? }
  end

  def service_payload(name, service, organization:, environment:)
    image = service["image"].to_s.strip.presence || image_reference_for(organization)
    payload = {
      name: name,
      kind: service_kind(service),
      image: image,
      entrypoint: string_array(service["command"]),
      command: string_array(service["args"]),
      env: service["env"].presence,
      secretRefs: merged_secret_refs_for_agent(service, service_name: name, environment: environment).presence,
      volumeMounts: service_volume_mounts(service, environment: environment).presence
    }.compact

    ports = desired_state_ports(service)
    payload[:ports] = ports if ports.present?
    healthcheck = service_healthcheck(service)
    payload[:healthcheck] = healthcheck if healthcheck.present?

    payload
  end

  def task_payload(name, service, organization:, environment:, secret_service_name: nil)
    image = service["image"].to_s.strip.presence || image_reference_for(organization)

    {
      name: name,
      image: image,
      entrypoint: string_array(service["command"]),
      command: string_array(service["args"]),
      env: service["env"].presence,
      secretRefs: resolved_task_secret_refs(service, secret_service_name:, environment:).presence,
      volumeMounts: service_volume_mounts(service, environment: environment).presence
    }.compact
  end

  def merged_task_service(service, task)
    stringify_hash(service).merge(
      "command" => task["command"].presence || service["command"],
      "args" => task["args"].presence || service["args"],
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

  def service_label(service)
    service_kind(service)
  end

  def string_array(value)
    return nil unless string_array?(value)
    return nil if value.empty?

    value.map(&:dup)
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
