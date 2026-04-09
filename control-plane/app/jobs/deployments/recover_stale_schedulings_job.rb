# frozen_string_literal: true

module Deployments
  class RecoverStaleSchedulingsJob < ApplicationJob
    queue_as :default

    def perform(now: Time.current)
      Deployments::StaleSchedulingRecovery.new(now:).call
    end
  end
end
