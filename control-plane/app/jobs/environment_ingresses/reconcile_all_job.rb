# frozen_string_literal: true

module EnvironmentIngresses
  class ReconcileAllJob < ApplicationJob
    queue_as :default

    def perform
      Environment.where(ingress_strategy: Environment::INGRESS_STRATEGY_DIRECT_DNS).find_each do |environment|
        EnvironmentIngresses::ReconcileJob.perform_later(environment.id)
      end
    end
  end
end
