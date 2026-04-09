# frozen_string_literal: true

require "test_helper"

class ActivityMailerTest < ActionMailer::TestCase
  test "hourly summary includes users and deployed hostnames" do
    mail = ActivityMailer.hourly_summary(
      recipient: "ops@example.com",
      users: [ "owner@example.com", "anonymous" ],
      deployed_hostnames: [ "shop.devopsellence.io" ],
      window_start: Time.utc(2026, 4, 2, 13, 0, 0),
      window_end: Time.utc(2026, 4, 2, 14, 0, 0)
    )

    assert_equal [ "ops@example.com" ], mail.to
    assert_equal "devopsellence hourly activity: 2 user signups, 1 deployed app", mail.subject
    assert_match "owner@example.com", mail.text_part.body.to_s
    assert_match "anonymous", mail.text_part.body.to_s
    assert_match "shop.devopsellence.io", mail.text_part.body.to_s
    assert_match "2026-04-02T13:00:00Z", mail.html_part.body.to_s
  end
end
