# frozen_string_literal: true

require "json"

class NodeDiagnoseRequest < ApplicationRecord
  CLAIM_TIMEOUT = 2.minutes
  STATUS_PENDING = "pending"
  STATUS_CLAIMED = "claimed"
  STATUS_COMPLETED = "completed"
  STATUS_FAILED = "failed"
  STATUSES = [
    STATUS_PENDING,
    STATUS_CLAIMED,
    STATUS_COMPLETED,
    STATUS_FAILED
  ].freeze

  belongs_to :node
  belongs_to :requested_by_user, class_name: "User"

  validates :status, inclusion: { in: STATUSES }
  validates :requested_at, presence: true

  scope :scrubbable_results, lambda { |cutoff:|
    where.not(result_json: [ nil, "" ]).where("completed_at <= ?", cutoff)
  }

  def self.find_or_create_active_for(node:, requested_by_user:, requested_at: Time.current)
    transaction do
      request = lock.where(node_id: node.id, status: [ STATUS_PENDING, STATUS_CLAIMED ])
        .order(:requested_at, :id)
        .first
      return request if request

      create_pending!(node:, requested_by_user:, requested_at:)
    end
  end

  def self.create_pending!(node:, requested_by_user:, requested_at: Time.current)
    create!(
      node: node,
      requested_by_user: requested_by_user,
      status: STATUS_PENDING,
      requested_at: requested_at
    )
  end

  def self.claim_pending_for(node:, now: Time.current)
    transaction do
      request = lock.where(node_id: node.id, status: STATUS_PENDING).order(:requested_at, :id).first
      if request.nil?
        request = lock.where(node_id: node.id, status: STATUS_CLAIMED)
          .where("claimed_at <= ?", now - CLAIM_TIMEOUT)
          .order(:requested_at, :id)
          .first
      end
      return nil unless request

      request.update!(
        status: STATUS_CLAIMED,
        claimed_at: now,
        completed_at: nil,
        error_message: nil,
        result_json: nil
      )
      request
    end
  end

  def pending?
    status == STATUS_PENDING
  end

  def claimed?
    status == STATUS_CLAIMED
  end

  def result_payload
    parsed = JSON.parse(result_json.presence || "null")
    parsed.is_a?(Hash) ? parsed : nil
  rescue JSON::ParserError
    nil
  end

  def result_payload=(value)
    self.result_json =
      if value.present?
        JSON.generate(value)
      end
  end

  def complete!(result:, completed_at: Time.current)
    update!(
      status: STATUS_COMPLETED,
      completed_at: completed_at,
      error_message: nil,
      result_payload: result
    )
  end

  def fail!(message:, completed_at: Time.current)
    update!(
      status: STATUS_FAILED,
      completed_at: completed_at,
      error_message: message,
      result_json: nil
    )
  end
end
