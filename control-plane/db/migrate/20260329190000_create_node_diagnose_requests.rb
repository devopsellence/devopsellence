class CreateNodeDiagnoseRequests < ActiveRecord::Migration[8.0]
  def change
    create_table :node_diagnose_requests do |t|
      t.references :node, null: false, foreign_key: true
      t.references :requested_by_user, null: false, foreign_key: { to_table: :users }
      t.string :status, null: false, default: "pending"
      t.datetime :requested_at, null: false
      t.datetime :claimed_at
      t.datetime :completed_at
      t.text :result_json
      t.text :error_message

      t.timestamps
    end

    add_index :node_diagnose_requests, [ :node_id, :status, :requested_at ], name: "index_node_diagnose_requests_on_claim_lookup"
    add_index :node_diagnose_requests, :completed_at
  end
end
