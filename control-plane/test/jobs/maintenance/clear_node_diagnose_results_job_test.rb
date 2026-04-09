# frozen_string_literal: true

require "test_helper"

module Maintenance
  class ClearNodeDiagnoseResultsJobTest < ActiveJob::TestCase
    include ActiveSupport::Testing::TimeHelpers

    test "clears stale diagnose payloads and keeps fresh ones" do
      user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
      organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
      node, = issue_test_node!(organization: organization, name: "node-a")

      stale_request = NodeDiagnoseRequest.create!(
        node: node,
        requested_by_user: user,
        status: NodeDiagnoseRequest::STATUS_COMPLETED,
        requested_at: 2.hours.ago,
        completed_at: 90.minutes.ago,
        result_json: { summary: { status: "ok" } }.to_json
      )
      fresh_request = NodeDiagnoseRequest.create!(
        node: node,
        requested_by_user: user,
        status: NodeDiagnoseRequest::STATUS_COMPLETED,
        requested_at: 20.minutes.ago,
        completed_at: 10.minutes.ago,
        result_json: { summary: { status: "ok" } }.to_json
      )

      Maintenance::ClearNodeDiagnoseResultsJob.perform_now

      assert_nil stale_request.reload.result_json
      assert_not_nil fresh_request.reload.result_json
    end
  end
end
