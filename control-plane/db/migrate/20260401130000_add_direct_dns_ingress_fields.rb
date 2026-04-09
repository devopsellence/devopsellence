class AddDirectDnsIngressFields < ActiveRecord::Migration[8.1]
  def change
    add_column :environments, :ingress_strategy, :string, null: false, default: "tunnel"
    add_index :environments, :ingress_strategy

    add_column :nodes, :capabilities_json, :text, null: false, default: "[]"
    add_column :nodes, :ingress_tls_status, :string, null: false, default: ""
    add_column :nodes, :ingress_tls_not_after, :datetime
    add_column :nodes, :ingress_tls_last_error, :text
  end
end
