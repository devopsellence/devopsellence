# frozen_string_literal: true

class OrganizationMembership < ApplicationRecord
  ROLE_OWNER = "owner"
  ROLE_CONTRIBUTOR = "contributor"
  ROLES = [ ROLE_OWNER, ROLE_CONTRIBUTOR ].freeze

  belongs_to :organization
  belongs_to :user

  validates :role, presence: true, inclusion: { in: ROLES }
  validates :user_id, uniqueness: { scope: :organization_id }

  def owner?
    role == ROLE_OWNER
  end
end
