# frozen_string_literal: true

module Deployments
  class StaleSchedulingRecovery
    STALE_AFTER = 90.seconds

    def initialize(scope: Deployment.all, now: Time.current, stale_after: STALE_AFTER, job_class: Deployments::PublishJob)
      @scope = scope
      @now = now
      @stale_after = stale_after
      @job_class = job_class
    end

    def call
      recovered = 0

      Runtime::AdvisoryLock.with_lock("deployments/stale_scheduling_recovery") do
        stale_scope.find_each do |deployment|
          recovered += 1 if recover_deployment!(deployment)
        end
      end

      recovered
    end

    private

    attr_reader :scope, :now, :stale_after, :job_class

    def stale_scope
      scope
        .where(status: Deployment::STATUS_SCHEDULING, finished_at: nil)
        .where("updated_at <= ?", now - stale_after)
    end

    def recover_deployment!(deployment)
      deployment.with_lock do
        deployment.reload
        return false unless stale?(deployment)

        Rails.logger.warn(
          "[deployments/stale_scheduling_recovery] re-enqueue deployment=#{deployment.id} " \
          "environment=#{deployment.environment_id} age=#{(now - deployment.updated_at).round(1)}s"
        )
        deployment.update!(status_message: "retrying stalled deployment scheduling")
        job_class.perform_later(deployment.id)
        true
      end
    end

    def stale?(deployment)
      deployment.status == Deployment::STATUS_SCHEDULING &&
        deployment.finished_at.blank? &&
        deployment.updated_at <= now - stale_after
    end
  end
end
