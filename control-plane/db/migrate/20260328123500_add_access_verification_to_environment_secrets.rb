class AddAccessVerificationToEnvironmentSecrets < ActiveRecord::Migration[8.1]
  def change
    add_column :environment_secrets, :access_grantee_email, :string
    add_column :environment_secrets, :access_verified_at, :datetime
  end
end
