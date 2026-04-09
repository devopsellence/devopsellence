# frozen_string_literal: true

require "test_helper"

class ApiTokenTest < ActiveSupport::TestCase
  include ActiveSupport::Testing::TimeHelpers

  test "touch_last_used_at_if_stale updates blank timestamp" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    token, = ApiToken.issue!(user: user)
    token.update_column(:last_used_at, nil)

    travel_to Time.zone.parse("2026-03-28 12:00:00 UTC") do
      token.touch_last_used_at_if_stale!
    end

    assert_equal Time.zone.parse("2026-03-28 12:00:00 UTC"), token.reload.last_used_at
  end

  test "touch_last_used_at_if_stale skips frequent poll churn" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    token, = ApiToken.issue!(user: user)

    travel_to Time.zone.parse("2026-03-28 12:00:00 UTC") do
      token.update_column(:last_used_at, Time.current)
    end

    travel_to Time.zone.parse("2026-03-28 12:00:30 UTC") do
      token.touch_last_used_at_if_stale!
    end

    assert_equal Time.zone.parse("2026-03-28 12:00:00 UTC"), token.reload.last_used_at
  end

  test "touch_last_used_at_if_stale refreshes old timestamp" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    token, = ApiToken.issue!(user: user)

    travel_to Time.zone.parse("2026-03-28 12:00:00 UTC") do
      token.update_column(:last_used_at, Time.current)
    end

    travel_to Time.zone.parse("2026-03-28 12:01:30 UTC") do
      token.touch_last_used_at_if_stale!
    end

    assert_equal Time.zone.parse("2026-03-28 12:01:30 UTC"), token.reload.last_used_at
  end
end
