# frozen_string_literal: true

class ReplaceReleaseRuntimeColumnsWithRuntimeJson < ActiveRecord::Migration[8.1]
  def up
    add_column :releases, :runtime_json, :text, default: "{}", null: false unless column_exists?(:releases, :runtime_json)
    remove_column :releases, :web_json if column_exists?(:releases, :web_json)
    remove_column :releases, :worker_json if column_exists?(:releases, :worker_json)
    remove_column :releases, :release_command if column_exists?(:releases, :release_command)
  end

  def down
    add_column :releases, :web_json, :text, default: "{}", null: false unless column_exists?(:releases, :web_json)
    add_column :releases, :worker_json, :text, default: "{}", null: false unless column_exists?(:releases, :worker_json)
    add_column :releases, :release_command, :string unless column_exists?(:releases, :release_command)
    remove_column :releases, :runtime_json if column_exists?(:releases, :runtime_json)
  end
end
