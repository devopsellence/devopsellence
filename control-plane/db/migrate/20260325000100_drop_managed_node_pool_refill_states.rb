# frozen_string_literal: true

class DropManagedNodePoolRefillStates < ActiveRecord::Migration[8.1]
  def change
    drop_table :managed_node_pool_refill_states
  end
end
