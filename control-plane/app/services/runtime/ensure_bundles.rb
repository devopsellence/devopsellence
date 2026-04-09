# frozen_string_literal: true

module Runtime
  module EnsureBundles
    extend self

    def enqueue
      EnsureBundlesJob.perform_later.successfully_enqueued?
    end
  end
end
