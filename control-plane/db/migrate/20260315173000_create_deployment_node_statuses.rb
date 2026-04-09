# frozen_string_literal: true

class CreateDeploymentNodeStatuses < ActiveRecord::Migration[8.1]
  def change
    create_table :deployment_node_statuses do |t|
      t.references :deployment, null: false, foreign_key: true
      t.references :node, null: false, foreign_key: true
      t.string :phase, null: false, default: "pending"
      t.text :message
      t.text :error_message
      t.text :containers_json
      t.datetime :reported_at

      t.timestamps
    end

    add_index :deployment_node_statuses, [ :deployment_id, :node_id ], unique: true
    add_index :deployment_node_statuses, [ :node_id, :updated_at ]
  end
end
