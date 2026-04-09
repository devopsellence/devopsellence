# frozen_string_literal: true

class AddBootstrapTokenToWarmBundles < ActiveRecord::Migration[8.1]
  def change
    add_column :warm_bundles, :bootstrap_token_id, :integer
    add_index  :warm_bundles, :bootstrap_token_id
    add_foreign_key :warm_bundles, :node_bootstrap_tokens, column: :bootstrap_token_id
  end
end
