# frozen_string_literal: true

module Deployments
  class Scheduler
    Result = Struct.new(:deployment, :scheduled, keyword_init: true)

    def initialize(environment:, release:, request_token:, job_class: Deployments::PublishJob)
      @environment = environment
      @release = release
      @request_token = request_token
      @job_class = job_class
    end

    def call
      scheduled = false
      deployment = nil

      Environment.transaction do
        environment.lock!
        release.lock!

        deployment = environment.deployments.find_by(request_token: request_token)
        next if deployment

        sequence = environment.deployments.maximum(:sequence).to_i + 1
        deployment = environment.deployments.create!(
          release: release,
          sequence: sequence,
          request_token: request_token,
          status: Deployment::STATUS_SCHEDULING,
          status_message: initial_status_message,
          published_at: Time.current
        )
        scheduled = true
      end

      job_class.perform_later(deployment.id) if scheduled
      Result.new(deployment: deployment, scheduled: scheduled)
    end

    private

    attr_reader :environment, :release, :request_token, :job_class

    def initial_status_message
      return "waiting to run release task" if release.has_release_task?
      if environment.managed_runtime?
        "waiting for managed capacity"
      else
        "waiting to publish desired state"
      end
    end
  end
end
