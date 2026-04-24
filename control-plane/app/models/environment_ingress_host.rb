# frozen_string_literal: true

class EnvironmentIngressHost < ApplicationRecord
  belongs_to :environment_ingress

  before_validation :normalize_hostname!

  validates :hostname, presence: true, uniqueness: true
  validates :position, numericality: { greater_than_or_equal_to: 0 }

  private
    def normalize_hostname!
      self.hostname = IngressHostnames.normalize(hostname)
    end
end
