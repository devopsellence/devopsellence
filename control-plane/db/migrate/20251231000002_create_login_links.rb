# frozen_string_literal: true

class CreateLoginLinks < ActiveRecord::Migration[8.1]
  def change
    create_table :login_links do |t|
      t.references :user, null: false, foreign_key: true
      t.string :token_digest, null: false
      t.datetime :expires_at, null: false
      t.datetime :consumed_at
      t.string :ip_address
      t.string :user_agent
      t.string :redirect_path
      t.string :redirect_uri
      t.string :state
      t.string :code_challenge
      t.string :code_challenge_method
      t.string :auth_code_digest
      t.datetime :auth_code_expires_at
      t.datetime :auth_code_consumed_at

      t.timestamps
    end

    add_index :login_links, :token_digest, unique: true
    add_index :login_links, :auth_code_digest, unique: true
    add_index :login_links, :expires_at
  end
end
