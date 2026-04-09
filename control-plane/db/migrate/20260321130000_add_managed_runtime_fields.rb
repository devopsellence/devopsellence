class AddManagedRuntimeFields < ActiveRecord::Migration[8.1]
  def change
    change_table :environments, bulk: true do |t|
      t.string :runtime_kind, null: false, default: "managed"
      t.string :managed_provider, null: false, default: "hetzner"
      t.string :managed_region, null: false, default: "ash"
      t.string :managed_size_slug, null: false, default: "cpx11"
    end

    change_table :node_bootstrap_tokens, bulk: true do |t|
      t.string :purpose, null: false, default: "manual"
      t.string :managed_provider
      t.string :managed_region
      t.string :managed_size_slug
      t.string :provider_server_id
      t.string :public_ip
      t.integer :node_id
    end

    change_table :nodes, bulk: true do |t|
      t.boolean :managed, null: false, default: false
      t.string :managed_provider
      t.string :managed_region
      t.string :managed_size_slug
      t.string :provider_server_id
      t.string :public_ip
    end

    add_index :node_bootstrap_tokens, :purpose
    add_index :node_bootstrap_tokens, :node_id
    add_index :nodes, [ :managed, :managed_provider, :managed_region, :managed_size_slug, :environment_id, :revoked_at ], name: "index_nodes_on_managed_capacity_lookup"
    add_foreign_key :node_bootstrap_tokens, :nodes
  end
end
