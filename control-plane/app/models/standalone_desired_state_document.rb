# frozen_string_literal: true

class StandaloneDesiredStateDocument < ApplicationRecord
  belongs_to :node
  belongs_to :node_bundle
  belongs_to :environment, optional: true

  validates :sequence, numericality: { greater_than_or_equal_to: 0 }
  validates :etag, presence: true
  validates :sha256, presence: true
  validates :payload_json, presence: true, length: { maximum: 1.megabyte }
  validates :node_id, uniqueness: { scope: :sequence }
  validates :node_bundle_id, uniqueness: { scope: :sequence }
end
