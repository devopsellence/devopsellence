class AddServiceRuntimeAndNodeLabels < ActiveRecord::Migration[8.1]
  def change
    change_table :nodes, bulk: true do |t|
      t.text :labels_json, null: false, default: '["web"]'
    end

    change_table :releases, bulk: true do |t|
      t.text :runtime_json, null: false, default: "{}"
    end
  end
end
