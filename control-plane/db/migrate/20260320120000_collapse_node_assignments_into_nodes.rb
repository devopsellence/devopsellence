# frozen_string_literal: true

class CollapseNodeAssignmentsIntoNodes < ActiveRecord::Migration[8.1]
  def up
    add_reference :nodes, :environment, foreign_key: true
    add_column :nodes, :desired_state_sequence, :integer, null: false, default: 0

    execute <<~SQL
      UPDATE nodes
      SET environment_id = assignments.environment_id,
          desired_state_sequence = assignments.sequence
      FROM environment_node_assignments assignments
      WHERE assignments.node_id = nodes.id
    SQL

    remove_reference :node_bootstrap_tokens, :environment, foreign_key: true
    remove_column :nodes, :service_account_email, :string
    drop_table :environment_node_assignments
  end

  def down
    create_table :environment_node_assignments do |t|
      t.string :assignment_uri
      t.references :environment, null: false, foreign_key: true
      t.integer :identity_version, null: false, default: 0
      t.references :node, null: false, foreign_key: true
      t.integer :sequence, null: false, default: 0
      t.timestamps
    end

    add_index :environment_node_assignments, [ :environment_id, :node_id ], unique: true, name: "idx_on_environment_id_node_id_d34943fd18"

    execute <<~SQL
      INSERT INTO environment_node_assignments (environment_id, identity_version, node_id, sequence, created_at, updated_at)
      SELECT nodes.environment_id,
             COALESCE(environments.identity_version, 0),
             nodes.id,
             nodes.desired_state_sequence,
             CURRENT_TIMESTAMP,
             CURRENT_TIMESTAMP
      FROM nodes
      LEFT JOIN environments ON environments.id = nodes.environment_id
      WHERE nodes.environment_id IS NOT NULL
    SQL

    add_column :nodes, :service_account_email, :string, null: false, default: ""
    execute <<~SQL
      UPDATE nodes
      SET service_account_email = COALESCE(environments.service_account_email, '')
      FROM environments
      WHERE environments.id = nodes.environment_id
    SQL
    add_reference :node_bootstrap_tokens, :environment, foreign_key: true
    remove_column :nodes, :desired_state_sequence
    remove_reference :nodes, :environment, foreign_key: true
  end
end
