# frozen_string_literal: true

class CreateProjectEnvironmentReleaseModels < ActiveRecord::Migration[8.1]
  def change
    create_table :projects do |t|
      t.references :organization, null: false, foreign_key: true
      t.string :slug, null: false
      t.string :name, null: false

      t.timestamps
    end
    add_index :projects, [ :organization_id, :slug ], unique: true

    create_table :environments do |t|
      t.references :project, null: false, foreign_key: true
      t.string :slug, null: false
      t.string :name, null: false
      t.string :gcp_project_id, null: false
      t.string :gcp_project_number, null: false
      t.string :workload_identity_pool, null: false
      t.string :workload_identity_provider, null: false
      t.string :service_account_email, null: false
      t.integer :identity_version, null: false, default: 1

      t.timestamps
    end
    add_index :environments, [ :project_id, :slug ], unique: true

    create_table :releases do |t|
      t.references :project, null: false, foreign_key: true
      t.string :git_sha, null: false
      t.string :image_digest, null: false
      t.text :desired_state_json, null: false
      t.string :desired_state_uri
      t.string :desired_state_sha256
      t.string :status, null: false, default: "draft"
      t.datetime :published_at

      t.timestamps
    end
    add_index :releases, [ :project_id, :created_at ]
    add_index :releases, [ :project_id, :git_sha ]

    add_reference :environments, :current_release, foreign_key: { to_table: :releases }

    create_table :deployments do |t|
      t.references :environment, null: false, foreign_key: true
      t.references :release, null: false, foreign_key: true
      t.integer :sequence, null: false
      t.string :status, null: false, default: "published"
      t.datetime :published_at, null: false
      t.datetime :finished_at
      t.text :error_message

      t.timestamps
    end
    add_index :deployments, [ :environment_id, :sequence ], unique: true
    add_index :deployments, [ :environment_id, :release_id ]

    create_table :environment_node_assignments do |t|
      t.references :environment, null: false, foreign_key: true
      t.references :node, null: false, foreign_key: true
      t.integer :sequence, null: false, default: 0
      t.integer :identity_version, null: false, default: 0
      t.string :assignment_uri

      t.timestamps
    end
    add_index :environment_node_assignments, [ :environment_id, :node_id ], unique: true
  end
end
