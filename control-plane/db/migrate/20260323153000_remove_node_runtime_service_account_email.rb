# frozen_string_literal: true

class RemoveNodeRuntimeServiceAccountEmail < ActiveRecord::Migration[8.0]
  def change
    remove_column :nodes, :runtime_service_account_email, :string
  end
end
