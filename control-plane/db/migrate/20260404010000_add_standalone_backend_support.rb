# frozen_string_literal: true

class AddStandaloneBackendSupport < ActiveRecord::Migration[8.1]
  def change
    add_column :runtime_projects, :runtime_backend, :string, default: "gcp", null: false

    add_column :environment_secrets, :value, :text
    add_column :environment_bundles, :tunnel_token, :text

    create_table :organization_registry_configs do |t|
      t.references :organization, null: false, foreign_key: true, index: { unique: true }
      t.string :registry_host, null: false
      t.string :repository_namespace, null: false
      t.string :username, null: false
      t.text :password, null: false
      t.datetime :expires_at
      t.timestamps
    end

    create_table :standalone_desired_state_documents do |t|
      t.references :node, null: false, foreign_key: true
      t.references :node_bundle, null: false, foreign_key: true
      t.references :environment, foreign_key: true
      t.integer :sequence, null: false
      t.string :etag, null: false
      t.string :sha256, null: false
      t.text :payload_json, null: false
      t.timestamps
    end

    add_index :standalone_desired_state_documents, [ :node_id, :sequence ], unique: true, name: "idx_standalone_desired_state_docs_on_node_and_sequence"
    add_index :standalone_desired_state_documents, [ :node_bundle_id, :sequence ], unique: true, name: "idx_standalone_desired_state_docs_on_bundle_and_sequence"
  end
end
