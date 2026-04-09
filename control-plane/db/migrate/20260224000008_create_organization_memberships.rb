# frozen_string_literal: true

class CreateOrganizationMemberships < ActiveRecord::Migration[8.1]
  def change
    create_table :organization_memberships do |t|
      t.references :organization, null: false, foreign_key: true
      t.references :user, null: false, foreign_key: true
      t.string :role, null: false

      t.timestamps
    end

    add_index :organization_memberships, [ :organization_id, :user_id ], unique: true
    add_index :organization_memberships, [ :user_id, :role ]
  end
end
