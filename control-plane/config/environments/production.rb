require "active_support/core_ext/integer/time"
require "ipaddr"
require "uri"
require Rails.root.join("lib/devopsellence/trusted_proxy_cidrs")

Rails.application.configure do
  # Settings specified here will take precedence over those in config/application.rb.

  # Code is not reloaded between requests.
  config.enable_reloading = false

  # Eager load code on boot for better performance and memory savings (ignored by Rake tasks).
  config.eager_load = true

  # Full error reports are disabled.
  config.consider_all_requests_local = false

  # Turn on fragment caching in view templates.
  config.action_controller.perform_caching = true

  # Cache assets for far-future expiry since they are all digest stamped.
  config.public_file_server.headers = { "cache-control" => "public, max-age=#{1.year.to_i}" }

  # Enable serving of images, stylesheets, and JavaScripts from an asset server.
  # config.asset_host = "http://assets.example.com"

  # Store uploaded files on the local file system (see config/storage.yml for options).
  config.active_storage.service = :local

  # Assume all access to the app is happening through a SSL-terminating reverse proxy.
  public_base_url = ENV["DEVOPSELLENCE_PUBLIC_BASE_URL"].to_s.strip.presence
  public_uri = public_base_url ? URI.parse(public_base_url) : nil
  # GFE-to-backend source ranges (not in cloud.json, which only covers customer-facing IPs)
  gfe_proxies = %w[35.191.0.0/16 130.211.0.0/22].map { |cidr| IPAddr.new(cidr) }
  gcp_cidrs = Devopsellence::TrustedProxyCidrs.load(path: Rails.root.join("config/gcp_cloud_cidrs.txt"))
  config.action_dispatch.trusted_proxies = ActionDispatch::RemoteIp::TRUSTED_PROXIES + gfe_proxies + gcp_cidrs
  if public_uri&.scheme == "https"
    config.assume_ssl = true
    config.force_ssl = true
  end

  # Force all access to the app over SSL, use Strict-Transport-Security, and use secure cookies.
  # config.force_ssl = true

  # Skip http-to-https redirect for the default health check endpoint.
  # config.ssl_options = { redirect: { exclude: ->(request) { request.path == "/up" } } }

  # Log to STDOUT with the current request id as a default log tag.
  config.log_tags = [ :request_id ]
  config.logger   = ActiveSupport::TaggedLogging.logger(STDOUT)

  # Change to "debug" to log everything (including potentially personally-identifiable information!).
  config.log_level = ENV.fetch("RAILS_LOG_LEVEL", "info")

  # Prevent health checks from clogging up the logs.
  config.silence_healthcheck_path = "/up"

  # Don't log any deprecations.
  config.active_support.report_deprecations = false

  # Replace the default in-process memory cache store with a durable alternative.
  config.cache_store = :solid_cache_store

  # Replace the default in-process and non-durable queuing backend for Active Job.
  config.active_job.queue_adapter = :solid_queue
  config.solid_queue.connects_to = { database: { writing: :queue } }

  # Ignore bad email addresses and do not raise email delivery errors.
  # Set this to true and configure the email server for immediate delivery to raise delivery errors.
  # config.action_mailer.raise_delivery_errors = false

  # Set host to be used by links generated in mailer templates.
  config.action_mailer.default_url_options = if public_uri
    options = { host: public_uri.host, protocol: public_uri.scheme }
    default_port = public_uri.scheme == "https" ? 443 : 80
    options[:port] = public_uri.port if public_uri.port != default_port
    options
  else
    { host: "example.com" }
  end
  resend_api_key = ENV["RESEND_API_KEY"].to_s.strip
  if resend_api_key.present?
    config.action_mailer.delivery_method = :smtp
    config.action_mailer.smtp_settings = {
      address: "smtp.resend.com",
      port: 587,
      user_name: "resend",
      password: resend_api_key,
      authentication: :plain,
      enable_starttls_auto: true
    }
  end

  # Enable locale fallbacks for I18n (makes lookups for any locale fall back to
  # the I18n.default_locale when a translation cannot be found).
  config.i18n.fallbacks = true

  # Do not dump schema after migrations.
  config.active_record.dump_schema_after_migration = false

  # Only use :id for inspections in production.
  config.active_record.attributes_for_inspect = [ :id ]

  # Enable DNS rebinding protection and other `Host` header attacks.
  config.hosts << public_uri.host if public_uri&.host
  ENV.fetch("DEVOPSELLENCE_ALLOWED_HOSTS", "").split(",").each do |host|
    normalized_host = host.to_s.strip
    config.hosts << normalized_host if normalized_host.present?
  end

  # Allow GCP health checks to probe /up without matching the public Host header.
  config.host_authorization = { exclude: ->(request) { request.path == "/up" } }
end
