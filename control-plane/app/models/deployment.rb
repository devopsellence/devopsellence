# frozen_string_literal: true

class Deployment < ApplicationRecord
  RELEASE_COMMAND_STATUS_PENDING = "pending"
  RELEASE_COMMAND_STATUS_RUNNING = "running"
  RELEASE_COMMAND_STATUS_SUCCEEDED = "succeeded"
  RELEASE_COMMAND_STATUS_FAILED = "failed"
  RELEASE_COMMAND_STATUSES = [
    RELEASE_COMMAND_STATUS_PENDING,
    RELEASE_COMMAND_STATUS_RUNNING,
    RELEASE_COMMAND_STATUS_SUCCEEDED,
    RELEASE_COMMAND_STATUS_FAILED
  ].freeze

  STATUS_SCHEDULING = "scheduling"
  STATUS_ROLLING_OUT = "rolling_out"
  STATUS_PUBLISHED = "published"
  STATUS_FAILED = "failed"
  STATUSES = [ STATUS_SCHEDULING, STATUS_ROLLING_OUT, STATUS_PUBLISHED, STATUS_FAILED ].freeze

  belongs_to :environment
  belongs_to :release
  belongs_to :release_command_node, class_name: "Node", optional: true
  has_many :deployment_node_statuses, dependent: :destroy

  validates :request_token, presence: true
  validates :sequence, numericality: { greater_than: 0 }
  validates :published_at, presence: true
  validates :status, inclusion: { in: STATUSES }
  validates :release_command_status, inclusion: { in: RELEASE_COMMAND_STATUSES }, allow_nil: true
  validates :sequence, uniqueness: { scope: :environment_id }
  validates :request_token, uniqueness: { scope: :environment_id }

  def release_command_active?
    [
      RELEASE_COMMAND_STATUS_PENDING,
      RELEASE_COMMAND_STATUS_RUNNING,
      RELEASE_COMMAND_STATUS_SUCCEEDED
    ].include?(release_command_status)
  end
end
