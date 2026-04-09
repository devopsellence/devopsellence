# frozen_string_literal: true

class CreateWarmBundles < ActiveRecord::Migration[8.1]
  def change
    create_table :warm_bundles do |t|
      t.string  :token,                 null: false
      t.string  :service_account_email, null: false
      t.references :runtime_project,    null: false, foreign_key: true
      t.integer :node_id
      t.string  :managed_provider,      null: false
      t.string  :managed_region,        null: false
      t.string  :managed_size_slug,     null: false
      t.string  :hostname
      t.string  :cloudflare_tunnel_id
      t.string  :gcp_secret_name
      t.string  :status,                null: false, default: "provisioning"
      t.integer :claimed_by_environment_id
      t.datetime :claimed_at
      t.datetime :provisioned_at
      t.text :provisioning_error
      t.timestamps
    end

    add_index :warm_bundles, :token, unique: true
    add_index :warm_bundles, :node_id, unique: true
    add_index :warm_bundles, :claimed_by_environment_id
    add_index :warm_bundles, [:status, :managed_provider, :managed_region, :managed_size_slug],
              name: "index_warm_bundles_on_status_and_pool"

    add_foreign_key :warm_bundles, :nodes
    add_foreign_key :warm_bundles, :environments, column: :claimed_by_environment_id
  end
end
