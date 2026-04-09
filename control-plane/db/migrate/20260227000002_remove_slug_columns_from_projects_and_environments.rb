# frozen_string_literal: true

class RemoveSlugColumnsFromProjectsAndEnvironments < ActiveRecord::Migration[8.1]
  def change
    remove_index :projects, column: [ :organization_id, :slug ], if_exists: true
    remove_column :projects, :slug, :string

    remove_index :environments, column: [ :project_id, :slug ], if_exists: true
    remove_column :environments, :slug, :string
  end
end
