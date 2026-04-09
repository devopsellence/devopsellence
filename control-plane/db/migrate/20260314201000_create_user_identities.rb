# frozen_string_literal: true

class CreateUserIdentities < ActiveRecord::Migration[8.1]
  def change
    create_table :user_identities do |t|
      t.references :user, null: false, foreign_key: true
      t.string :provider, null: false
      t.string :provider_uid, null: false
      t.string :email, null: false
      t.text :profile_json, null: false, default: "{}"
      t.datetime :last_used_at

      t.timestamps
    end

    add_index :user_identities, [ :provider, :provider_uid ], unique: true
    add_index :user_identities, [ :user_id, :provider ], unique: true
  end
end
