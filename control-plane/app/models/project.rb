# frozen_string_literal: true

class Project < ApplicationRecord
  belongs_to :organization

  has_many :environments, dependent: :destroy
  has_many :releases, dependent: :destroy

  validates :name, presence: true
  validates :name, uniqueness: { scope: :organization_id }
end
