# frozen_string_literal: true

class CreateApiTokens < ActiveRecord::Migration[8.1]
  def change
    create_table :api_tokens do |t|
      t.references :user, null: false, foreign_key: true
      t.string :access_token_digest, null: false
      t.string :refresh_token_digest, null: false
      t.datetime :access_expires_at, null: false
      t.datetime :refresh_expires_at, null: false
      t.datetime :revoked_at
      t.datetime :last_used_at

      t.timestamps
    end

    add_index :api_tokens, :access_token_digest, unique: true
    add_index :api_tokens, :refresh_token_digest, unique: true
    add_index :api_tokens, :access_expires_at
  end
end
