# frozen_string_literal: true

class ReaddEnvironmentToNodeBootstrapTokens < ActiveRecord::Migration[8.1]
  def up
    unless column_exists?(:node_bootstrap_tokens, :environment_id)
      add_reference :node_bootstrap_tokens, :environment, foreign_key: true
      return
    end

    add_index :node_bootstrap_tokens, :environment_id unless index_exists?(:node_bootstrap_tokens, :environment_id)
    add_foreign_key :node_bootstrap_tokens, :environments unless foreign_key_exists?(:node_bootstrap_tokens, :environments)
  end

  def down
    remove_reference :node_bootstrap_tokens, :environment, foreign_key: true if column_exists?(:node_bootstrap_tokens, :environment_id)
  end
end
