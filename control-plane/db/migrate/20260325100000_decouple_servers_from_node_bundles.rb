# frozen_string_literal: true

class DecoupleServersFromNodeBundles < ActiveRecord::Migration[8.1]
  def change
    remove_foreign_key :node_bundles, :node_bootstrap_tokens, column: :bootstrap_token_id, if_exists: true
    remove_index :node_bundles, :bootstrap_token_id, if_exists: true
    remove_column :node_bundles, :bootstrap_token_id, :integer
    remove_column :node_bundles, :managed_provider, :string
    remove_column :node_bundles, :managed_region, :string
    remove_column :node_bundles, :managed_size_slug, :string
  end
end
