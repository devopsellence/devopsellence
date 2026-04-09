# frozen_string_literal: true

require "google/apis/core"

# google-apis-core uses Rails.logger by default, which would dump full Faraday
# request/response objects including Authorization bearer tokens at DEBUG level.
# Give it a dedicated logger at WARN level so auth headers never appear in logs.
google_logger = Logger.new($stdout)
google_logger.level = Logger::WARN
Google::Apis.logger = google_logger
