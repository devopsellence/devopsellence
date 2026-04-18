# frozen_string_literal: true

module Deployments
  class PublishJob < ApplicationJob
    queue_as :default

    def perform(deployment_id = nil, **kwargs)
      deployment_id ||= kwargs[:deployment_id]
      deployment = nil
      Runtime::AdvisoryLock.with_lock("deployments/publish/#{deployment_id}") do
        deployment = Deployment.find_by(id: deployment_id)
        return unless deployment
        return if deployment.finished_at.present?
        return unless publishable?(deployment)

        Deployments::Publisher.new(
          environment: deployment.environment,
          release: deployment.release,
          deployment: deployment
        ).call
      end
    rescue *TRANSIENT_DATABASE_ERRORS => error
      Rails.logger.warn("[deployments] publish retry deployment=#{deployment_id}: #{error.class}: #{error.message}")
      raise
    rescue StandardError => error
      Rails.logger.error("[deployments] publish failed deployment=#{deployment_id}: #{error.class}: #{error.message}")
      deployment&.update!(
        status: Deployment::STATUS_FAILED,
        status_message: "publish failed",
        finished_at: Time.current,
        error_message: error.message
      )
    end

    private

    def publishable?(deployment)
      return true if deployment.status == Deployment::STATUS_SCHEDULING

      deployment.status == Deployment::STATUS_ROLLING_OUT &&
        deployment.release_task_status == Deployment::RELEASE_TASK_STATUS_SUCCEEDED
    end
  end
end
