class AddValueSha256ToEnvironmentSecrets < ActiveRecord::Migration[8.0]
  def change
    add_column :environment_secrets, :value_sha256, :string
  end
end
