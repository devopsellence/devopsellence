class RemoveInitJsonFromReleases < ActiveRecord::Migration[8.0]
  def change
    remove_column :releases, :init_json, :text, default: "{}", null: false
  end
end
