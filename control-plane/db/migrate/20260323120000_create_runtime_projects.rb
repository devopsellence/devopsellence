class CreateRuntimeProjects < ActiveRecord::Migration[8.1]
  def change
    create_table :runtime_projects do |t|
      t.string :name, null: false
      t.string :slug, null: false
      t.string :kind, null: false, default: "shared_sandbox"
      t.string :gcp_project_id, null: false
      t.string :gcp_project_number, null: false
      t.string :workload_identity_pool, null: false
      t.string :workload_identity_provider, null: false
      t.string :gar_region, null: false
      t.string :gcs_bucket_prefix, null: false
      t.timestamps
    end

    add_index :runtime_projects, :slug, unique: true
    add_reference :organizations, :runtime_project, foreign_key: true
    add_reference :environments, :runtime_project, foreign_key: true
  end
end
