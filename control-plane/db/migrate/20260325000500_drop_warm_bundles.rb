# frozen_string_literal: true

class DropWarmBundles < ActiveRecord::Migration[8.1]
  def up
    drop_table :warm_bundles
  end

  def down
    raise ActiveRecord::IrreversibleMigration, "warm_bundles has been removed"
  end
end
