# frozen_string_literal: true

class AddAnonymousAccountsAndTrialTiers < ActiveRecord::Migration[8.1]
  def up
    change_table :users, bulk: true do |t|
      t.string :account_kind, null: false, default: "human"
      t.string :anonymous_identifier
      t.string :anonymous_secret_digest
      t.datetime :claimed_at
    end

    remove_index :users, :email
    add_index :users, :email, unique: true, where: "email IS NOT NULL"
    add_index :users, :anonymous_identifier, unique: true, where: "anonymous_identifier IS NOT NULL"

    change_column_null :users, :email, true

    add_column :organizations, :plan_tier, :string, null: false, default: "paid"

    create_table :claim_links do |t|
      t.references :user, null: false, foreign_key: true
      t.string :email, null: false
      t.string :token_digest, null: false
      t.datetime :expires_at, null: false
      t.datetime :consumed_at
      t.string :ip_address
      t.string :user_agent

      t.timestamps
    end

    add_index :claim_links, :token_digest, unique: true
    add_index :claim_links, :expires_at
  end

  def down
    drop_table :claim_links

    remove_column :organizations, :plan_tier

    change_column_null :users, :email, false
    remove_index :users, :anonymous_identifier
    remove_index :users, :email
    add_index :users, :email, unique: true

    change_table :users, bulk: true do |t|
      t.remove :account_kind, :anonymous_identifier, :anonymous_secret_digest, :claimed_at
    end
  end
end
