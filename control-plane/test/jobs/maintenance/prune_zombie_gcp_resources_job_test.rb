# frozen_string_literal: true

require "test_helper"

module Maintenance
  class PruneZombieGcpResourcesJobTest < ActiveJob::TestCase
    test "runs the zombie gcp pruner" do
      pruner = mock("pruner")
      pruner.expects(:call).once
      Maintenance::PruneZombieGcpResources.expects(:new).returns(pruner)

      Maintenance::PruneZombieGcpResourcesJob.perform_now
    end
  end
end
