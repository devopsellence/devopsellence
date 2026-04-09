# frozen_string_literal: true

class ScopeNodesAndBootstrapTokensToOrganizations < ActiveRecord::Migration[8.1]
  class MigrationNode < ApplicationRecord
    self.table_name = "nodes"
  end

  class MigrationNodeBootstrapToken < ApplicationRecord
    self.table_name = "node_bootstrap_tokens"
  end

  class MigrationOrganization < ApplicationRecord
    self.table_name = "organizations"
  end

  class MigrationOrganizationMembership < ApplicationRecord
    self.table_name = "organization_memberships"
  end

  class MigrationUser < ApplicationRecord
    self.table_name = "users"
  end

  def up
    add_reference :nodes, :organization, foreign_key: true
    add_reference :node_bootstrap_tokens, :organization, foreign_key: true
    add_reference :node_bootstrap_tokens, :issued_by_user, foreign_key: { to_table: :users }

    user_organization_ids = {}

    say_with_time "creating personal organizations for existing users" do
      MigrationUser.find_each do |user|
        organization = MigrationOrganization.create!(name: "org-#{user.id}")
        MigrationOrganizationMembership.create!(
          organization_id: organization.id,
          user_id: user.id,
          role: "owner"
        )
        user_organization_ids[user.id] = organization.id
      end
    end

    say_with_time "backfilling organization_id on nodes" do
      MigrationNode.find_each do |node|
        organization_id = user_organization_ids[node.user_id]
        next unless organization_id

        node.update_columns(organization_id: organization_id)
      end
    end

    say_with_time "backfilling organization_id on node bootstrap tokens" do
      MigrationNodeBootstrapToken.find_each do |token|
        organization_id = user_organization_ids[token.user_id]
        next unless organization_id

        token.update_columns(organization_id: organization_id, issued_by_user_id: token.user_id)
      end
    end

    change_column_null :nodes, :organization_id, false
    change_column_null :node_bootstrap_tokens, :organization_id, false

    remove_reference :nodes, :user, foreign_key: true
    remove_reference :node_bootstrap_tokens, :user, foreign_key: true
  end

  def down
    raise ActiveRecord::IrreversibleMigration, "Cannot safely restore user scoped nodes/bootstrap tokens"
  end
end
