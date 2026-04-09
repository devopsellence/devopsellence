# frozen_string_literal: true

class CreateNodeBootstrapTokens < ActiveRecord::Migration[8.1]
  def change
    create_table :node_bootstrap_tokens do |t|
      t.references :user, null: false, foreign_key: true
      t.string :token_digest, null: false
      t.datetime :expires_at, null: false
      t.datetime :consumed_at

      t.timestamps
    end

    add_index :node_bootstrap_tokens, :token_digest, unique: true
    add_index :node_bootstrap_tokens, :expires_at
  end
end
