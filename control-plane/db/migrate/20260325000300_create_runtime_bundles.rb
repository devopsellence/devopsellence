class CreateRuntimeBundles < ActiveRecord::Migration[8.1]
  def change
    create_table :organization_bundles do |t|
      t.string :token, null: false
      t.references :runtime_project, null: false, foreign_key: true
      t.references :claimed_by_organization, foreign_key: { to_table: :organizations }
      t.string :gcs_bucket_name, null: false
      t.string :gar_repository_name, null: false
      t.string :gar_repository_region, null: false
      t.string :gar_writer_service_account_email, null: false
      t.string :status, null: false, default: "provisioning"
      t.datetime :claimed_at
      t.datetime :provisioned_at
      t.text :provisioning_error
      t.timestamps
    end

    add_index :organization_bundles, :token, unique: true
    add_index :organization_bundles, :gcs_bucket_name, unique: true
    add_index :organization_bundles, :gar_repository_name, unique: true
    add_index :organization_bundles, :gar_writer_service_account_email, unique: true, name: "index_org_bundles_on_writer_sa"
    add_index :organization_bundles, :status

    create_table :environment_bundles do |t|
      t.string :token, null: false
      t.references :runtime_project, null: false, foreign_key: true
      t.references :organization_bundle, null: false, foreign_key: true
      t.references :claimed_by_environment, foreign_key: { to_table: :environments }
      t.string :service_account_email, null: false
      t.string :gcp_secret_name, null: false
      t.string :hostname
      t.string :cloudflare_tunnel_id
      t.string :status, null: false, default: "provisioning"
      t.datetime :claimed_at
      t.datetime :provisioned_at
      t.text :provisioning_error
      t.timestamps
    end

    add_index :environment_bundles, :token, unique: true
    add_index :environment_bundles, :service_account_email, unique: true
    add_index :environment_bundles, :gcp_secret_name, unique: true
    add_index :environment_bundles, :hostname, unique: true
    add_index :environment_bundles, [ :organization_bundle_id, :status ], name: "index_env_bundles_on_org_bundle_and_status"

    create_table :node_bundles do |t|
      t.string :token, null: false
      t.references :runtime_project, null: false, foreign_key: true
      t.references :organization_bundle, null: false, foreign_key: true
      t.references :environment_bundle, null: false, foreign_key: true
      t.references :node, index: false, foreign_key: true
      t.references :bootstrap_token, foreign_key: { to_table: :node_bootstrap_tokens }
      t.string :managed_provider
      t.string :managed_region
      t.string :managed_size_slug
      t.string :status, null: false, default: "provisioning"
      t.datetime :claimed_at
      t.datetime :provisioned_at
      t.text :provisioning_error
      t.timestamps
    end

    add_index :node_bundles, :token, unique: true
    add_index :node_bundles, [ :environment_bundle_id, :status ], name: "index_node_bundles_on_env_bundle_and_status"
    add_index :node_bundles, :node_id, unique: true

    add_reference :organizations, :organization_bundle, foreign_key: true
    add_reference :environments, :environment_bundle, foreign_key: true
    add_reference :nodes, :node_bundle, foreign_key: true
  end
end
