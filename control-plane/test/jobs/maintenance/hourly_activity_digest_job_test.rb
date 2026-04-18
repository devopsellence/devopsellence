# frozen_string_literal: true

require "test_helper"

module Maintenance
  class HourlyActivityDigestJobTest < ActiveJob::TestCase
    include ActiveSupport::Testing::TimeHelpers

    setup do
      ActionMailer::Base.deliveries.clear
    end

    test "sends hourly digest with recent user signups and deployed hostnames" do
      now = Time.utc(2026, 4, 2, 15, 10, 0)
      cache = ActiveSupport::Cache::MemoryStore.new
      hostname = "shop.devopsellence.io"

      travel_to(now) do
        create_recent_human_user!(email: "owner@example.com", created_at: 40.minutes.ago)
        create_recent_anonymous_user!(identifier: "anon-123", created_at: 25.minutes.ago)
        create_published_deployment!(hostname:, finished_at: 15.minutes.ago)

        with_runtime_config(activity_notification_to: "ops@example.com") do
          Rails.stubs(:cache).returns(cache)

          assert_difference("ActionMailer::Base.deliveries.size", 1) do
            HourlyActivityDigestJob.perform_now(now: Time.current)
          end
        end
      end

      mail = ActionMailer::Base.deliveries.last
      assert_equal [ "ops@example.com" ], mail.to
      assert_match "owner@example.com", mail.text_part.body.to_s
      assert_match "anonymous", mail.text_part.body.to_s
      assert_match hostname, mail.text_part.body.to_s
    end

    test "skips delivery when there is no new activity" do
      cache = ActiveSupport::Cache::MemoryStore.new

      with_runtime_config(activity_notification_to: "ops@example.com") do
        Rails.stubs(:cache).returns(cache)

        assert_no_difference("ActionMailer::Base.deliveries.size") do
          HourlyActivityDigestJob.perform_now(now: Time.utc(2026, 4, 2, 15, 10, 0))
        end
      end
    end

    test "does not resend the same hour bucket after a successful delivery" do
      now = Time.utc(2026, 4, 2, 15, 10, 0)
      cache = ActiveSupport::Cache::MemoryStore.new

      travel_to(now) do
        create_recent_human_user!(email: "owner@example.com", created_at: 35.minutes.ago)

        with_runtime_config(activity_notification_to: "ops@example.com") do
          Rails.stubs(:cache).returns(cache)

          assert_difference("ActionMailer::Base.deliveries.size", 1) do
            HourlyActivityDigestJob.perform_now(now: Time.current)
          end

          assert_no_difference("ActionMailer::Base.deliveries.size") do
            HourlyActivityDigestJob.perform_now(now: Time.current)
          end
        end
      end
    end

    private
      def create_recent_human_user!(email:, created_at:)
        user = User.create!(email: email, confirmed_at: Time.current)
        user.update_columns(created_at: created_at, updated_at: created_at)
        user
      end

      def create_recent_anonymous_user!(identifier:, created_at:)
        user = User.bootstrap_anonymous!(identifier: identifier, raw_secret: "secret-123")
        user.update_columns(created_at: created_at, updated_at: created_at)
        user
      end

      def create_published_deployment!(hostname:, finished_at:)
        organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
        project = organization.projects.create!(name: "shop-app")
        environment = project.environments.create!(name: "production")
        release = project.releases.create!(
          git_sha: "a" * 40,
          revision: "rev-1",
          image_repository: "shop-app",
          image_digest: "sha256:#{"b" * 64}",
          runtime_json: release_runtime_json
        )
        environment.create_environment_ingress!(
          hostname: hostname,
          gcp_secret_name: "ingress-#{SecureRandom.hex(4)}",
          status: EnvironmentIngress::STATUS_READY
        )

        environment.deployments.create!(
          release: release,
          sequence: 1,
          request_token: SecureRandom.hex(16),
          status: Deployment::STATUS_PUBLISHED,
          status_message: "rollout settled",
          published_at: finished_at - 5.minutes,
          finished_at: finished_at
        )
      end
  end
end
