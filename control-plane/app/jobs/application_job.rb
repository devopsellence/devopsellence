class ApplicationJob < ActiveJob::Base
  TRANSIENT_DATABASE_ERRORS = [
    ActiveRecord::DatabaseConnectionError,
    ActiveRecord::ConnectionNotEstablished
  ].freeze

  retry_on(*TRANSIENT_DATABASE_ERRORS, wait: 10.seconds, attempts: 12)

  # Automatically retry jobs that encountered a deadlock
  # retry_on ActiveRecord::Deadlocked

  # Most jobs are safe to ignore if the underlying records are no longer available
  # discard_on ActiveJob::DeserializationError
end
