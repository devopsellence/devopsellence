# frozen_string_literal: true

class CreateEnvironmentIngresses < ActiveRecord::Migration[8.1]
  def change
    create_table :environment_ingresses do |t|
      t.references :environment, null: false, foreign_key: true, index: { unique: true }
      t.string :hostname, null: false
      t.string :cloudflare_tunnel_id, null: false, default: ""
      t.string :gcp_secret_name, null: false
      t.string :status, null: false, default: "pending"
      t.text :last_error
      t.datetime :provisioned_at
      t.timestamps
    end

    add_index :environment_ingresses, :hostname, unique: true
    add_index :environment_ingresses, :gcp_secret_name, unique: true
  end
end
