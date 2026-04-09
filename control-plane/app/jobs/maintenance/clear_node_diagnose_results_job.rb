# frozen_string_literal: true

module Maintenance
  class ClearNodeDiagnoseResultsJob < ApplicationJob
    def perform(cutoff: 1.hour.ago)
      count = NodeDiagnoseRequest.scrubbable_results(cutoff: cutoff).update_all(result_json: nil, updated_at: Time.current)
      Rails.logger.info("[maintenance/clear_node_diagnose_results_job] scrubbed #{count} diagnose result(s)") if count > 0
    end
  end
end
