# frozen_string_literal: true

class RenameReleaseCommandStateToReleaseTask < ActiveRecord::Migration[8.1]
  def up
    rename_column :deployments, :release_command_status, :release_task_status if column_exists?(:deployments, :release_command_status) && !column_exists?(:deployments, :release_task_status)
    add_column :deployments, :release_task_status, :string unless column_exists?(:deployments, :release_task_status)

    if column_exists?(:deployments, :release_command_node_id) && !column_exists?(:deployments, :release_task_node_id)
      remove_foreign_key :deployments, column: :release_command_node_id if foreign_key_exists?(:deployments, column: :release_command_node_id)
      rename_column :deployments, :release_command_node_id, :release_task_node_id
      rename_index_if_present("index_deployments_on_release_command_node_id", "index_deployments_on_release_task_node_id")
    end
    add_reference :deployments, :release_task_node, foreign_key: { to_table: :nodes } unless column_exists?(:deployments, :release_task_node_id)
    add_index :deployments, :release_task_node_id, name: "index_deployments_on_release_task_node_id" unless index_exists?(:deployments, :release_task_node_id)
    add_foreign_key :deployments, :nodes, column: :release_task_node_id unless foreign_key_exists?(:deployments, column: :release_task_node_id)
  end

  def down
    rename_column :deployments, :release_task_status, :release_command_status if column_exists?(:deployments, :release_task_status) && !column_exists?(:deployments, :release_command_status)
    add_column :deployments, :release_command_status, :string unless column_exists?(:deployments, :release_command_status)

    if column_exists?(:deployments, :release_task_node_id) && !column_exists?(:deployments, :release_command_node_id)
      remove_foreign_key :deployments, column: :release_task_node_id if foreign_key_exists?(:deployments, column: :release_task_node_id)
      rename_column :deployments, :release_task_node_id, :release_command_node_id
      rename_index_if_present("index_deployments_on_release_task_node_id", "index_deployments_on_release_command_node_id")
    end
    add_reference :deployments, :release_command_node, foreign_key: { to_table: :nodes } unless column_exists?(:deployments, :release_command_node_id)
    add_index :deployments, :release_command_node_id, name: "index_deployments_on_release_command_node_id" unless index_exists?(:deployments, :release_command_node_id)
    add_foreign_key :deployments, :nodes, column: :release_command_node_id unless foreign_key_exists?(:deployments, column: :release_command_node_id)
  end

  private

  def rename_index_if_present(from, to)
    return unless connection.indexes(:deployments).any? { |index| index.name == from }
    return if connection.indexes(:deployments).any? { |index| index.name == to }

    rename_index :deployments, from, to
  end
end
