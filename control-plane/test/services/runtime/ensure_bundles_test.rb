# frozen_string_literal: true

require "test_helper"

module Runtime
  class EnsureBundlesTest < ActiveSupport::TestCase
    include ActiveJob::TestHelper

    setup do
      clear_enqueued_jobs
    end

    test "enqueue schedules the refill job on the bundle_refill queue" do
      assert_enqueued_with(job: Runtime::EnsureBundlesJob, queue: "bundle_refill") do
        assert_equal true, Runtime::EnsureBundles.enqueue
      end
    end

    test "enqueue reports false when Solid Queue discards a conflicting refill job" do
      fake_job = Struct.new(:successfully_enqueued?).new(false)

      Runtime::EnsureBundlesJob.stubs(:perform_later).returns(fake_job)
      assert_no_enqueued_jobs only: Runtime::EnsureBundlesJob do
        assert_equal false, Runtime::EnsureBundles.enqueue
      end
    end
  end
end
