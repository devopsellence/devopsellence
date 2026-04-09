# frozen_string_literal: true

class RemoveLegacyGarRegionDefault < ActiveRecord::Migration[8.1]
  def change
    change_column_default :organizations, :gar_repository_region, from: "us-east1", to: ""
  end
end
