# frozen_string_literal: true

module Maintenance
  class PruneZombieGcpResourcesJob < ApplicationJob
    queue_as :default
    limits_concurrency key: -> { "global" }, group: "Maintenance::PruneZombieGcpResources", duration: 30.minutes, on_conflict: :discard

    def perform
      Maintenance::PruneZombieGcpResources.new.call
    end
  end
end
