class AddAsyncPublishFieldsToDeployments < ActiveRecord::Migration[8.0]
  def change
    add_column :deployments, :request_token, :string
    add_column :deployments, :status_message, :string

    Deployment.reset_column_information
    say_with_time "backfill deployment request tokens" do
      Deployment.find_each do |deployment|
        deployment.update_columns(
          request_token: deployment.request_token.presence || SecureRandom.hex(16),
          status_message: deployment.status_message.presence || default_status_message_for(deployment)
        )
      end
    end

    change_column_null :deployments, :request_token, false
    add_index :deployments, [ :environment_id, :request_token ], unique: true
  end

  private

  def default_status_message_for(deployment)
    case deployment.status
    when Deployment::STATUS_FAILED
      "publish failed"
    else
      "waiting for node reconcile"
    end
  end
end
