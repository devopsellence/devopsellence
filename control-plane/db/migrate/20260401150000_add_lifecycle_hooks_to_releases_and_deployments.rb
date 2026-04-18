class AddLifecycleHooksToReleasesAndDeployments < ActiveRecord::Migration[8.0]
  def change
    add_column :deployments, :release_task_status, :string
    add_reference :deployments, :release_task_node, foreign_key: { to_table: :nodes }
  end
end
