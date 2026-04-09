# frozen_string_literal: true

module Runtime
  class EnsureBundlesJob < ApplicationJob
    queue_as :bundle_refill
    limits_concurrency key: -> { "global" }, group: "Runtime::EnsureBundles", duration: 15.minutes, on_conflict: :discard

    def perform
      Runtime::BundlesReconciler.new.call
    end
  end
end
