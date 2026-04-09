# frozen_string_literal: true

class UpdateNodesForRefreshTokens < ActiveRecord::Migration[8.1]
  def change
    rename_column :nodes, :token_digest, :access_token_digest

    add_column :nodes, :refresh_token_digest, :string, null: false, default: ""
    add_column :nodes, :access_expires_at, :datetime
    add_column :nodes, :refresh_expires_at, :datetime
    add_column :nodes, :revoked_at, :datetime

    if index_name_exists?(:nodes, "index_nodes_on_token_digest")
      remove_index :nodes, name: "index_nodes_on_token_digest"
    else
      remove_index :nodes, :token_digest if index_exists?(:nodes, :token_digest)
    end

    add_index :nodes, :access_token_digest, unique: true unless index_exists?(:nodes, :access_token_digest)
    add_index :nodes, :refresh_token_digest, unique: true unless index_exists?(:nodes, :refresh_token_digest)
  end
end
