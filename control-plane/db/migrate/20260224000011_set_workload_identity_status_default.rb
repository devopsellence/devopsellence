# frozen_string_literal: true

class SetWorkloadIdentityStatusDefault < ActiveRecord::Migration[8.1]
  def change
    change_column_default :organization_workload_identities, :status, from: "ready", to: "pending_manual"
  end
end
