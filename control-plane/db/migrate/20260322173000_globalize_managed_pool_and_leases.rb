# frozen_string_literal: true

class GlobalizeManagedPoolAndLeases < ActiveRecord::Migration[8.1]
  def change
    change_column_null :nodes, :organization_id, true
    add_column :nodes, :lease_expires_at, :datetime
    add_index :nodes, :lease_expires_at

    change_column_null :node_bootstrap_tokens, :organization_id, true

    remove_index :nodes, name: "index_nodes_on_managed_capacity_lookup"
    add_index :nodes,
      [ :managed, :managed_provider, :managed_region, :managed_size_slug, :organization_id, :environment_id, :revoked_at ],
      name: "index_nodes_on_managed_capacity_lookup"
  end
end
