# frozen_string_literal: true

class CreateNodes < ActiveRecord::Migration[8.1]
  def change
    create_table :nodes do |t|
      t.references :user, null: false, foreign_key: true
      t.string :name
      t.string :token_digest, null: false
      t.datetime :last_seen_at

      t.timestamps
    end

    add_index :nodes, :token_digest, unique: true
  end
end
