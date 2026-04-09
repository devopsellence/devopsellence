class AddControlPlaneGcpMvpRuntimeFields < ActiveRecord::Migration[8.1]
  def change
    change_table :organizations, bulk: true do |t|
      t.string :gcp_project_id, null: false, default: ""
      t.string :gcp_project_number, null: false, default: ""
      t.string :workload_identity_pool, null: false, default: ""
      t.string :workload_identity_provider, null: false, default: ""
      t.string :gcs_bucket_name, null: false, default: ""
      t.string :gar_repository_name, null: false, default: ""
      t.string :gar_repository_region, null: false, default: "us-east1"
      t.string :provisioning_status, null: false, default: "pending_manual"
      t.text :provisioning_error
    end

    change_table :nodes, bulk: true do |t|
      t.string :desired_state_bucket, null: false, default: ""
      t.string :desired_state_object_path, null: false, default: ""
      t.string :service_account_email, null: false, default: ""
      t.string :provisioning_status, null: false, default: "pending_manual"
      t.text :provisioning_error
    end

    change_table :releases, bulk: true do |t|
      t.string :image_repository, null: false, default: ""
      t.string :entrypoint
      t.string :command
      t.text :env_json, null: false, default: "{}"
      t.text :secret_refs_json, null: false, default: "[]"
      t.string :healthcheck_path
      t.integer :healthcheck_port
      t.integer :healthcheck_interval_seconds, null: false, default: 5
      t.integer :healthcheck_timeout_seconds, null: false, default: 2
      t.string :revision
    end

    change_column_null :releases, :desired_state_json, true
  end
end
