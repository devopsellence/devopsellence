# frozen_string_literal: true

class CreateManagedNodePoolRefillStates < ActiveRecord::Migration[8.1]
  def change
    create_table :managed_node_pool_refill_states do |t|
      t.string :pool_key, null: false
      t.integer :target_count, null: false, default: 0
      t.boolean :requested, null: false, default: false
      t.boolean :running, null: false, default: false
      t.datetime :last_requested_at
      t.datetime :last_started_at
      t.datetime :last_finished_at

      t.timestamps
    end

    add_index :managed_node_pool_refill_states, :pool_key, unique: true
  end
end
