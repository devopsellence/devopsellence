class AddLifecycleHooksToReleasesAndDeployments < ActiveRecord::Migration[8.0]
  def change
    add_column :releases, :init_json, :text, default: "{}", null: false
    add_column :releases, :release_command, :string

    add_column :deployments, :release_command_status, :string
    add_reference :deployments, :release_command_node, foreign_key: { to_table: :nodes }
  end
end
