# frozen_string_literal: true

class RemoveManualProvisioningDefaults < ActiveRecord::Migration[8.1]
  def change
    change_column_default :organizations, :provisioning_status, from: "pending_manual", to: "failed"
    change_column_default :nodes, :provisioning_status, from: "pending_manual", to: "failed"
    change_column_default :organization_workload_identities, :status, from: "pending_manual", to: "failed"
  end
end
