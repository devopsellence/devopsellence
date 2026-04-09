# frozen_string_literal: true

require "json"

class DeploymentNodeStatus < ApplicationRecord
  PHASE_PENDING = "pending"
  PHASE_RECONCILING = "reconciling"
  PHASE_SETTLED = "settled"
  PHASE_ERROR = "error"
  PHASES = [
    PHASE_PENDING,
    PHASE_RECONCILING,
    PHASE_SETTLED,
    PHASE_ERROR
  ].freeze

  belongs_to :deployment
  belongs_to :node

  validates :phase, inclusion: { in: PHASES }
  validates :node_id, uniqueness: { scope: :deployment_id }

  def containers
    parsed = JSON.parse(containers_json.presence || "[]")
    parsed.is_a?(Array) ? parsed : []
  rescue JSON::ParserError
    []
  end

  def containers=(value)
    self.containers_json = JSON.generate(Array(value))
  end
end
