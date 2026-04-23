# frozen_string_literal: true

class EnvironmentIngressHost < ApplicationRecord
  belongs_to :environment_ingress

  validates :hostname, presence: true, uniqueness: true
  validates :position, numericality: { greater_than_or_equal_to: 0 }
end
