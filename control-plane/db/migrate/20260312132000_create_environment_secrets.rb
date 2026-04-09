class CreateEnvironmentSecrets < ActiveRecord::Migration[8.1]
  def change
    create_table :environment_secrets do |t|
      t.references :environment, null: false, foreign_key: true
      t.string :service_name, null: false
      t.string :name, null: false
      t.string :gcp_secret_name, null: false

      t.timestamps
    end

    add_index :environment_secrets, [ :environment_id, :service_name, :name ], unique: true, name: "idx_environment_secrets_on_scope"
    add_index :environment_secrets, :gcp_secret_name, unique: true
  end
end
