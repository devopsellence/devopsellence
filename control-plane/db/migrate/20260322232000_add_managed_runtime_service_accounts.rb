# frozen_string_literal: true

class AddManagedRuntimeServiceAccounts < ActiveRecord::Migration[8.1]
  def change
    add_column :nodes, :runtime_service_account_email, :string
    change_column_null :environments, :service_account_email, true
  end
end
