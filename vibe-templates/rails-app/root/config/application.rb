require_relative "boot"

require "rails/all"

Bundler.require(*Rails.groups)

module {{APP_MODULE}}
  class Application < Rails::Application
    config.load_defaults 8.1

    config.autoload_lib(ignore: %w[assets tasks])
    config.active_job.queue_adapter = :solid_queue
  end
end
