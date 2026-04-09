# frozen_string_literal: true

module Maintenance
  class HourlyActivityDigestJob < ApplicationJob
    WINDOW = 1.hour
    CACHE_TTL = 2.days

    def perform(now: Time.current)
      recipients = activity_notification_recipients

      if recipients.empty?
        return
      end

      window_end = now.beginning_of_hour
      window_start = window_end - WINDOW

      Runtime::AdvisoryLock.with_lock(lock_name(window_start)) do
        if delivery_recorded?(window_start)
          return
        end

        users = recent_user_signups(window_start:, window_end:)
        deployed_hostnames = recent_deployed_hostnames(window_start:, window_end:)

        if users.empty? && deployed_hostnames.empty?
          return
        end

        ActivityMailer.hourly_summary(
          recipient: recipients,
          users: users,
          deployed_hostnames: deployed_hostnames,
          window_start: window_start,
          window_end: window_end
        ).deliver_now

        record_delivery!(window_start)
      end
    end

    private
      def activity_notification_recipients
        Devopsellence::RuntimeConfig.current.activity_notification_to.to_s.split(",").filter_map do |entry|
          normalized = entry.to_s.strip
          normalized.presence
        end
      end

      def recent_user_signups(window_start:, window_end:)
        User.where(created_at: window_start...window_end)
          .order(:created_at, :id)
          .map do |user|
            if user.anonymous?
              "anonymous"
            else
              user.email
            end
          end
      end

      def recent_deployed_hostnames(window_start:, window_end:)
        Deployment.joins(environment: :environment_ingress)
          .where(status: Deployment::STATUS_PUBLISHED, finished_at: window_start...window_end)
          .distinct
          .order("environment_ingresses.hostname ASC")
          .pluck("environment_ingresses.hostname")
      end

      def lock_name(window_start)
        "maintenance/hourly_activity_digest/#{window_start.utc.iso8601}"
      end

      def delivery_recorded?(window_start)
        delivery_cache.read(cache_key(window_start)).present?
      end

      def record_delivery!(window_start)
        delivery_cache.write(cache_key(window_start), true, expires_in: CACHE_TTL)
      end

      def cache_key(window_start)
        "maintenance/hourly_activity_digest/sent/#{window_start.utc.iso8601}"
      end

      def delivery_cache
        Rails.cache
      end
  end
end
