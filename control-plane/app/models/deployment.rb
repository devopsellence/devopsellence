# frozen_string_literal: true

class Deployment < ApplicationRecord
  RELEASE_TASK_STATUS_PENDING = "pending"
  RELEASE_TASK_STATUS_RUNNING = "running"
  RELEASE_TASK_STATUS_SUCCEEDED = "succeeded"
  RELEASE_TASK_STATUS_FAILED = "failed"
  RELEASE_TASK_STATUSES = [
    RELEASE_TASK_STATUS_PENDING,
    RELEASE_TASK_STATUS_RUNNING,
    RELEASE_TASK_STATUS_SUCCEEDED,
    RELEASE_TASK_STATUS_FAILED
  ].freeze

  STATUS_SCHEDULING = "scheduling"
  STATUS_ROLLING_OUT = "rolling_out"
  STATUS_PUBLISHED = "published"
  STATUS_FAILED = "failed"
  STATUSES = [ STATUS_SCHEDULING, STATUS_ROLLING_OUT, STATUS_PUBLISHED, STATUS_FAILED ].freeze

  belongs_to :environment
  belongs_to :release
  belongs_to :release_task_node, class_name: "Node", optional: true
  has_many :deployment_node_statuses, dependent: :destroy

  validates :request_token, presence: true
  validates :sequence, numericality: { greater_than: 0 }
  validates :published_at, presence: true
  validates :status, inclusion: { in: STATUSES }
  validates :release_task_status, inclusion: { in: RELEASE_TASK_STATUSES }, allow_nil: true
  validates :sequence, uniqueness: { scope: :environment_id }
  validates :request_token, uniqueness: { scope: :environment_id }

  def release_task_active?
    [
      RELEASE_TASK_STATUS_PENDING,
      RELEASE_TASK_STATUS_RUNNING,
      RELEASE_TASK_STATUS_SUCCEEDED
    ].include?(release_task_status)
  end
end
