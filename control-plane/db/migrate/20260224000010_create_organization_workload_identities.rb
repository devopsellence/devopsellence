# frozen_string_literal: true

class CreateOrganizationWorkloadIdentities < ActiveRecord::Migration[8.1]
  def change
    create_table :organization_workload_identities do |t|
      t.references :organization, null: false, foreign_key: true
      t.references :created_by_user, foreign_key: { to_table: :users }
      t.string :project_slug, null: false
      t.string :gcp_project_id, null: false
      t.string :gcp_project_number, null: false
      t.string :service_account_email, null: false
      t.string :workload_identity_pool, null: false
      t.string :workload_identity_provider, null: false
      t.string :status, null: false, default: "ready"
      t.text :last_error

      t.timestamps
    end

    add_index :organization_workload_identities, [ :organization_id, :project_slug ], unique: true,
      name: "index_org_workload_identities_on_org_and_project"
  end
end
