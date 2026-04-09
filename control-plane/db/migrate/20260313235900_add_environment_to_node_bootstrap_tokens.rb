class AddEnvironmentToNodeBootstrapTokens < ActiveRecord::Migration[8.0]
  def change
    add_reference :node_bootstrap_tokens, :environment, foreign_key: true
  end
end
