class AddDesiredStateFieldsToNodeBundles < ActiveRecord::Migration[8.1]
  def up
    add_column :node_bundles, :desired_state_object_path, :string, null: false, default: ""
    add_column :node_bundles, :desired_state_sequence, :integer, null: false, default: 0

    execute <<~SQL
      UPDATE node_bundles
      SET desired_state_object_path = 'node-bundles/' || token || '/desired_state.json'
      WHERE desired_state_object_path = ''
    SQL
  end

  def down
    remove_column :node_bundles, :desired_state_sequence
    remove_column :node_bundles, :desired_state_object_path
  end
end
