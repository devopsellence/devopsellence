# frozen_string_literal: true

class RenameContainersJsonToEnvironmentsJson < ActiveRecord::Migration[8.1]
  def change
    rename_column :deployment_node_statuses, :containers_json, :environments_json
  end
end
