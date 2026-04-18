# frozen_string_literal: true

require "json"

class ReplaceReleaseRuntimeColumnsWithRuntimeJson < ActiveRecord::Migration[8.1]
  def up
    add_column :releases, :runtime_json, :text, default: "{}", null: false unless column_exists?(:releases, :runtime_json)
    backfill_runtime_json
    remove_column :releases, :web_json if column_exists?(:releases, :web_json)
    remove_column :releases, :worker_json if column_exists?(:releases, :worker_json)
    remove_column :releases, :release_command if column_exists?(:releases, :release_command)
  end

  def down
    add_column :releases, :web_json, :text, default: "{}", null: false unless column_exists?(:releases, :web_json)
    add_column :releases, :worker_json, :text, default: "{}", null: false unless column_exists?(:releases, :worker_json)
    add_column :releases, :release_command, :string unless column_exists?(:releases, :release_command)
    remove_column :releases, :runtime_json if column_exists?(:releases, :runtime_json)
  end

  private

  def backfill_runtime_json
    return unless column_exists?(:releases, :runtime_json)
    return unless column_exists?(:releases, :web_json) || column_exists?(:releases, :worker_json) || column_exists?(:releases, :release_command)

    say_with_time "Backfilling releases.runtime_json from legacy runtime columns" do
      rows = select_all(<<~SQL.squish)
        SELECT id,
               #{column_exists?(:releases, :web_json) ? "web_json" : "NULL AS web_json"},
               #{column_exists?(:releases, :worker_json) ? "worker_json" : "NULL AS worker_json"},
               #{column_exists?(:releases, :release_command) ? "release_command" : "NULL AS release_command"}
        FROM releases
      SQL
      rows.each do |row|
        runtime = legacy_runtime_payload(
          web: parse_json_object(row["web_json"]),
          worker: parse_json_object(row["worker_json"]),
          release_command: row["release_command"]
        )
        next if runtime["services"].blank? && runtime["tasks"].blank?

        execute(<<~SQL.squish)
          UPDATE releases
          SET runtime_json = #{quote(JSON.generate(runtime))}
          WHERE id = #{quote(row["id"])}
        SQL
      end
      rows.length
    end
  end

  def legacy_runtime_payload(web:, worker:, release_command:)
    services = {}
    services["web"] = legacy_web_service(web) if web.present?
    services["worker"] = legacy_worker_service(worker) if worker.present?

    tasks = {}
    command = release_command.to_s.strip
    if command.present? && services.key?("web")
      tasks["release"] = {
        "service" => "web",
        "command" => command
      }
    end

    payload = {
      "services" => services,
      "tasks" => tasks
    }
    payload["ingress_service"] = "web" if services.key?("web")
    payload
  end

  def legacy_web_service(service)
    port = positive_integer(service["port"]) || 3000
    healthcheck = service["healthcheck"].is_a?(Hash) ? service["healthcheck"] : {}

    legacy_service(service).merge(
      "kind" => "web",
      "roles" => [ "web" ],
      "ports" => [ { "name" => "http", "port" => port } ],
      "healthcheck" => {
        "path" => healthcheck["path"].to_s.presence || "/",
        "port" => positive_integer(healthcheck["port"]) || port
      }
    )
  end

  def legacy_worker_service(service)
    legacy_service(service).merge(
      "kind" => "worker",
      "roles" => [ "worker" ]
    )
  end

  def legacy_service(service)
    {
      "entrypoint" => present_string(service["entrypoint"]),
      "command" => present_string(service["command"]),
      "env" => service["env"].is_a?(Hash) ? service["env"] : nil,
      "secret_refs" => service["secret_refs"].is_a?(Array) ? service["secret_refs"] : nil,
      "volumes" => service["volumes"].is_a?(Array) ? service["volumes"] : nil
    }.compact
  end

  def parse_json_object(value)
    parsed = JSON.parse(value.presence || "{}")
    parsed.is_a?(Hash) ? stringify_keys(parsed) : {}
  rescue JSON::ParserError, TypeError
    {}
  end

  def stringify_keys(value)
    value.each_with_object({}) do |(key, entry), result|
      result[key.to_s] =
        case entry
        when Hash
          stringify_keys(entry)
        when Array
          entry.map { |item| item.is_a?(Hash) ? stringify_keys(item) : item }
        else
          entry
        end
    end
  end

  def present_string(value)
    value.is_a?(String) ? value.strip.presence : nil
  end

  def positive_integer(value)
    integer = Integer(value)
    integer.positive? ? integer : nil
  rescue ArgumentError, TypeError
    nil
  end
end
