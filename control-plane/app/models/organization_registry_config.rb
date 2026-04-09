# frozen_string_literal: true

class OrganizationRegistryConfig < ApplicationRecord
  belongs_to :organization

  encrypts :password

  validates :registry_host, presence: true
  validates :repository_namespace, presence: true
  validates :username, presence: true
  validates :password, presence: true

  def repository_path
    [ registry_host.to_s.strip, repository_namespace.to_s.strip.sub(%r{\A/+}, "") ].reject(&:blank?).join("/")
  end
end
