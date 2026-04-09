require "minitest/autorun"
require "active_support/all"
require "active_support/configuration_file"
require "pathname"

class ProductionQueuePoolSizingTest < Minitest::Test
  ROOT = Pathname(__dir__).join("../..").expand_path

  def setup
    @original_env = ENV.to_h.slice("QUEUE_DATABASE_MAX_CONNECTIONS", "JOB_THREADS", "SOLID_QUEUE_IN_PUMA")
  end

  def teardown
    %w[QUEUE_DATABASE_MAX_CONNECTIONS JOB_THREADS SOLID_QUEUE_IN_PUMA].each do |key|
      ENV[key] = @original_env[key]
    end
  end

  def test_production_queue_pool_default_covers_solid_queue_supervisor_threads
    ENV.delete("QUEUE_DATABASE_MAX_CONNECTIONS")
    ENV.delete("JOB_THREADS")
    ENV["SOLID_QUEUE_IN_PUMA"] = "true"

    required_pool = required_queue_pool_size
    configured_pool = configured_queue_pool_size

    assert_equal 4, required_pool
    assert_equal 4, configured_pool
    assert_operator configured_pool, :>=, required_pool
  end

  def test_production_queue_pool_override_still_wins
    ENV["QUEUE_DATABASE_MAX_CONNECTIONS"] = "7"
    ENV["JOB_THREADS"] = "2"
    ENV["SOLID_QUEUE_IN_PUMA"] = "true"

    assert_equal 7, configured_queue_pool_size
  end

  private

  def configured_queue_pool_size
    parse_config("config/database.yml")
      .fetch("production")
      .fetch("queue")
      .fetch("max_connections")
      .to_i
  end

  def required_queue_pool_size
    workers = parse_config("config/queue.yml")
      .fetch("production")
      .fetch("workers")

    worker_threads = workers.map { |worker| Integer(worker.fetch("threads", 3)) }
    worker_threads.max + 2
  end

  def parse_config(path)
    ActiveSupport::ConfigurationFile.parse(ROOT.join(path))
  end
end
