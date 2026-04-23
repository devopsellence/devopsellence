# frozen_string_literal: true

class CreateEnvironmentIngressHosts < ActiveRecord::Migration[8.1]
  class MigrationEnvironmentIngress < ApplicationRecord
    self.table_name = "environment_ingresses"
  end

  def up
    create_table :environment_ingress_hosts do |t|
      t.references :environment_ingress, null: false, foreign_key: true
      t.string :hostname, null: false
      t.integer :position, null: false, default: 0
      t.timestamps
    end

    add_index :environment_ingress_hosts, :hostname, unique: true
    add_index :environment_ingress_hosts, [ :environment_ingress_id, :position ], unique: true, name: "index_env_ingress_hosts_on_ingress_and_position"

    MigrationEnvironmentIngress.reset_column_information
    MigrationEnvironmentIngress.find_each do |ingress|
      hostname = ingress.hostname.to_s.strip
      next if hostname.blank?

      execute <<~SQL.squish
        INSERT INTO environment_ingress_hosts (environment_ingress_id, hostname, position, created_at, updated_at)
        VALUES (#{ingress.id}, #{connection.quote(hostname)}, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
      SQL
    end
  end

  def down
    drop_table :environment_ingress_hosts
  end
end
