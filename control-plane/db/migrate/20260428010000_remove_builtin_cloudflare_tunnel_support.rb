# frozen_string_literal: true

class RemoveBuiltinCloudflareTunnelSupport < ActiveRecord::Migration[8.1]
  def up
    change_column_default :environments, :ingress_strategy, from: "tunnel", to: "direct_dns"
    execute "UPDATE environments SET ingress_strategy = 'direct_dns' WHERE ingress_strategy = 'tunnel'"

    remove_column :environment_bundles, :cloudflare_tunnel_id, :string
    remove_column :environment_bundles, :gcp_secret_name, :string
    remove_column :environment_bundles, :tunnel_token, :text

    remove_column :environment_ingresses, :cloudflare_tunnel_id, :string, default: "", null: false
    remove_column :environment_ingresses, :gcp_secret_name, :string
  end

  def down
    raise ActiveRecord::IrreversibleMigration, "built-in Cloudflare Tunnel support was removed"
  end
end
