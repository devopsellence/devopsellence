# frozen_string_literal: true

class ReplaceOrgWorkloadIdentityProjectSlugWithProjectId < ActiveRecord::Migration[8.1]
  def change
    add_reference :organization_workload_identities, :project, foreign_key: true
    remove_index :organization_workload_identities, name: "index_org_workload_identities_on_org_and_project", if_exists: true
    add_index :organization_workload_identities, [ :organization_id, :project_id ], unique: true,
      where: "project_id IS NOT NULL",
      name: "index_org_workload_identities_on_org_and_project"
    remove_column :organization_workload_identities, :project_slug, :string
  end
end
