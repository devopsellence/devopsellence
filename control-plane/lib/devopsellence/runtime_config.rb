# frozen_string_literal: true

require "active_support/ordered_options"
require_relative "development_env_defaults"
require_relative "test_env_defaults"

module Devopsellence
  module RuntimeConfig
    BACKEND_GCP = "gcp"
    BACKEND_STANDALONE = "standalone"
    BACKENDS = [
      BACKEND_GCP,
      BACKEND_STANDALONE
    ].freeze
    DEFAULT_CLOUDFLARE_ACCOUNT_ID = ""
    DEFAULT_CLOUDFLARE_ZONE_ID = ""
    DEFAULT_CLOUDFLARE_ZONE_NAME = "devopsellence.io"
    DEFAULT_CLOUDFLARE_ENVOY_ORIGIN = "http://devopsellence-envoy:8000"
    DEFAULT_ACME_CONTACT_EMAIL = "admin@devopsellence.com"
    DEFAULT_ACME_DIRECTORY_URL = "https://acme-v02.api.letsencrypt.org/directory"
    DEFAULT_ACME_STAGING_DIRECTORY_URL = "https://acme-staging-v02.api.letsencrypt.org/directory"
    DEFAULT_MANAGED_PROVIDER = "hetzner"
    DEFAULT_MANAGED_REGION = "ash"
    DEFAULT_MANAGED_SIZE = "cpx11"
    DEFAULT_MANAGED_REGISTRATION_TIMEOUT_SECONDS = "180"
    DEFAULT_MANAGED_LEASE_MINUTES = "60"
    DEFAULT_MANAGED_POOL_TARGET = "1"
    DEFAULT_ORGANIZATION_BUNDLE_TARGET = "1"
    DEFAULT_ENVIRONMENT_BUNDLE_TARGET = "1"
    DEFAULT_NODE_BUNDLE_TARGET = "1"
    DEFAULT_MANAGED_MAX_TOTAL = "5"
    DEFAULT_HETZNER_IMAGE = "ubuntu-24.04"

    REQUIRED_KEYS = {
      gcp_project_id: "DEVOPSELLENCE_DEFAULT_GCP_PROJECT_ID",
      gcp_project_number: "DEVOPSELLENCE_DEFAULT_GCP_PROJECT_NUMBER",
      workload_identity_pool: "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_POOL",
      workload_identity_provider: "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_PROVIDER",
      gar_region: "DEVOPSELLENCE_DEFAULT_GAR_REGION",
      gcs_bucket_prefix: "DEVOPSELLENCE_GCS_BUCKET_PREFIX"
    }.freeze
    OPTIONAL_DEFAULTS = {
      runtime_backend: ["DEVOPSELLENCE_RUNTIME_BACKEND", BACKEND_GCP],
      control_plane_service_account_email: ["DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_EMAIL", ""],
      control_plane_service_account_id: ["DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_ID", "devopsellence-control-plane"],
      control_plane_service_account_project_id: ["DEVOPSELLENCE_CONTROL_PLANE_SERVICE_ACCOUNT_PROJECT_ID", ""],
      public_base_url: ["DEVOPSELLENCE_PUBLIC_BASE_URL", ""],
      ingress_backend: ["DEVOPSELLENCE_INGRESS_BACKEND", "cloudflare"],
      local_ingress_public_url: ["DEVOPSELLENCE_LOCAL_INGRESS_PUBLIC_URL", ""],
      local_ingress_hostname_suffix: ["DEVOPSELLENCE_LOCAL_INGRESS_HOSTNAME_SUFFIX", "local.devopsellence.test"],
      cloudflare_account_id: ["CLOUDFLARE_ACCOUNT_ID", DEFAULT_CLOUDFLARE_ACCOUNT_ID],
      cloudflare_zone_id: ["CLOUDFLARE_ZONE_ID", DEFAULT_CLOUDFLARE_ZONE_ID],
      cloudflare_zone_name: ["CLOUDFLARE_ZONE_NAME", DEFAULT_CLOUDFLARE_ZONE_NAME],
      mail_from_name: ["MAIL_FROM_NAME", ""],
      mail_from_address: ["MAIL_FROM_ADDRESS", "noreply@example.com"],
      activity_notification_to: ["DEVOPSELLENCE_ACTIVITY_NOTIFICATION_TO", ""],
      http_basic_username: ["DEVOPSELLENCE_HTTP_BASIC_USERNAME", ""],
      http_basic_password: ["DEVOPSELLENCE_HTTP_BASIC_PASSWORD", ""],
      google_client_id: ["GOOGLE_CLIENT_ID", ""],
      google_client_secret: ["GOOGLE_CLIENT_SECRET", ""],
      github_client_id: ["GITHUB_CLIENT_ID", ""],
      github_client_secret: ["GITHUB_CLIENT_SECRET", ""],
      gcs_bucket: ["DEVOPSELLENCE_GCS_BUCKET", ""],
      gcs_prefix: ["DEVOPSELLENCE_GCS_PREFIX", ""],
      gcs_endpoint: ["DEVOPSELLENCE_GCS_ENDPOINT", "https://storage.googleapis.com"],
      sts_endpoint: ["DEVOPSELLENCE_STS_ENDPOINT", "https://sts.googleapis.com/v1/token"],
      secret_manager_endpoint: ["DEVOPSELLENCE_SECRET_MANAGER_ENDPOINT", "https://secretmanager.googleapis.com/v1"],
      artifact_registry_endpoint: ["DEVOPSELLENCE_ARTIFACT_REGISTRY_ENDPOINT", "https://artifactregistry.googleapis.com/v1"],
      artifact_registry_download_endpoint: ["DEVOPSELLENCE_ARTIFACT_REGISTRY_DOWNLOAD_ENDPOINT", "https://artifactregistry.googleapis.com/download/v1"],
      iam_endpoint: ["DEVOPSELLENCE_IAM_ENDPOINT", "https://iam.googleapis.com/v1"],
      iam_credentials_endpoint: ["DEVOPSELLENCE_IAM_CREDENTIALS_ENDPOINT", "https://iamcredentials.googleapis.com/v1"],
      cloud_kms_endpoint: ["DEVOPSELLENCE_CLOUD_KMS_ENDPOINT", "https://cloudkms.googleapis.com/v1"],
      gar_host_override: ["DEVOPSELLENCE_GAR_HOST_OVERRIDE", ""],
      cloudflare_api_token: ["CLOUDFLARE_API_TOKEN", ""],
      cloudflare_envoy_origin: ["DEVOPSELLENCE_CLOUDFLARE_ENVOY_ORIGIN", DEFAULT_CLOUDFLARE_ENVOY_ORIGIN],
      acme_account_key_path: ["DEVOPSELLENCE_ACME_ACCOUNT_KEY_PATH", Rails.root.join("tmp/acme-account-key.pem").to_s],
      acme_contact_email: ["DEVOPSELLENCE_ACME_CONTACT_EMAIL", DEFAULT_ACME_CONTACT_EMAIL],
      acme_directory_url: ["DEVOPSELLENCE_ACME_DIRECTORY_URL", DEFAULT_ACME_DIRECTORY_URL],
      stable_version: ["DEVOPSELLENCE_STABLE_VERSION", ""],
      agent_container_image: ["DEVOPSELLENCE_AGENT_CONTAINER_IMAGE", ""],
      agent_container_repository: ["DEVOPSELLENCE_AGENT_CONTAINER_REPOSITORY", ""],
      managed_default_provider: ["DEVOPSELLENCE_MANAGED_DEFAULT_PROVIDER", DEFAULT_MANAGED_PROVIDER],
      managed_default_region: ["DEVOPSELLENCE_MANAGED_DEFAULT_REGION", DEFAULT_MANAGED_REGION],
      managed_default_size_slug: ["DEVOPSELLENCE_MANAGED_DEFAULT_SIZE", DEFAULT_MANAGED_SIZE],
      managed_registration_timeout_seconds: ["DEVOPSELLENCE_MANAGED_REGISTRATION_TIMEOUT_SECONDS", DEFAULT_MANAGED_REGISTRATION_TIMEOUT_SECONDS],
      managed_lease_minutes: ["DEVOPSELLENCE_MANAGED_LEASE_MINUTES", DEFAULT_MANAGED_LEASE_MINUTES],
      managed_pool_target: ["DEVOPSELLENCE_MANAGED_POOL_TARGET", DEFAULT_MANAGED_POOL_TARGET],
      organization_bundle_target: ["DEVOPSELLENCE_ORGANIZATION_BUNDLE_TARGET", DEFAULT_ORGANIZATION_BUNDLE_TARGET],
      environment_bundle_target: ["DEVOPSELLENCE_ENVIRONMENT_BUNDLE_TARGET", DEFAULT_ENVIRONMENT_BUNDLE_TARGET],
      node_bundle_target: ["DEVOPSELLENCE_NODE_BUNDLE_TARGET", DEFAULT_NODE_BUNDLE_TARGET],
      managed_max_total: ["DEVOPSELLENCE_MANAGED_MAX_TOTAL", DEFAULT_MANAGED_MAX_TOTAL],
      hetzner_api_token: ["DEVOPSELLENCE_HETZNER_API_TOKEN", ""],
      digitalocean_api_token: ["DEVOPSELLENCE_DIGITALOCEAN_API_TOKEN", ""],
      bundle_provisioning_timeout_seconds: ["DEVOPSELLENCE_BUNDLE_PROVISIONING_TIMEOUT_SECONDS", "600"],
      hetzner_ssh_key_name: ["DEVOPSELLENCE_HETZNER_SSH_KEY_NAME", ""],
      hetzner_ssh_public_key: ["DEVOPSELLENCE_HETZNER_SSH_PUBLIC_KEY", ""],
      hetzner_default_image: ["DEVOPSELLENCE_HETZNER_IMAGE", DEFAULT_HETZNER_IMAGE],
      digitalocean_default_image: ["DEVOPSELLENCE_DIGITALOCEAN_DEFAULT_IMAGE", "ubuntu-24-04-x64"],
      digitalocean_ssh_key_name: ["DEVOPSELLENCE_DIGITALOCEAN_SSH_KEY_NAME", ""],
      digitalocean_ssh_public_key: ["DEVOPSELLENCE_DIGITALOCEAN_SSH_PUBLIC_KEY", ""]
    }.freeze

    MissingEnvironmentError = Class.new(StandardError)
    InvalidEnvironmentError = Class.new(StandardError)

    module_function

    def load_current!(env: ENV, rails_env: Rails.env)
      load!(env: effective_env(env:, rails_env:))
    end

    def load!(env: ENV)
      backend = env.fetch("DEVOPSELLENCE_RUNTIME_BACKEND", BACKEND_GCP).to_s.strip.presence || BACKEND_GCP
      missing = required_keys_for(backend).values.select { |key| env[key].to_s.strip.empty? }
      raise MissingEnvironmentError, "Missing required runtime env vars: #{missing.join(', ')}" if missing.any?

      ActiveSupport::OrderedOptions.new.tap do |config|
        REQUIRED_KEYS.each do |name, key|
          config[name] = env.fetch(key, "").to_s.strip
        end
        OPTIONAL_DEFAULTS.each do |name, (key, fallback)|
          config[name] = env.fetch(key, fallback).to_s.strip
        end
        config.managed_pool_candidates = build_managed_pool_candidates(config)
        validate_runtime_backend!(config)
        validate_workload_identity_resource_names!(config)
        validate_public_base_url!(config)
        validate_public_base_url_scheme!(config)
        validate_ingress_backend!(config)
      end
    end

    def current
      Rails.application.config.x.devopsellence_runtime || raise(MissingEnvironmentError, "Runtime config is not loaded")
    end

    def effective_env(env: ENV, rails_env: Rails.env)
      env_hash = env.to_h
      env_hash = env_hash.merge(TestEnvDefaults::ENV) if rails_env.to_s == "test"
      env_hash = DevelopmentEnvDefaults::ENV.merge(env_hash) if rails_env.to_s == "development"
      if rails_env.to_s == "development" && env_hash["DEVOPSELLENCE_ACME_DIRECTORY_URL"].to_s.strip.empty?
        env_hash["DEVOPSELLENCE_ACME_DIRECTORY_URL"] = DEFAULT_ACME_STAGING_DIRECTORY_URL
      end
      env_hash
    end

    def build_managed_pool_candidates(config)
      default_candidate = normalize_pool_candidate(
        provider_slug: config.managed_default_provider,
        region: config.managed_default_region,
        size_slug: config.managed_default_size_slug
      )

      [default_candidate, *default_managed_pool_fallbacks(default_candidate)].uniq
    end

    def normalize_pool_candidate(provider_slug:, region:, size_slug:)
      {
        provider_slug: provider_slug.to_s.strip,
        region: region.to_s.strip,
        size_slug: size_slug.to_s.strip
      }
    end

    def default_managed_pool_fallbacks(default_candidate)
      return [] unless default_candidate[:provider_slug] == "hetzner"

      case default_candidate[:region]
      when "ash"
        [normalize_pool_candidate(provider_slug: "hetzner", region: "hil", size_slug: default_candidate[:size_slug])]
      when "hil"
        [normalize_pool_candidate(provider_slug: "hetzner", region: "ash", size_slug: default_candidate[:size_slug])]
      else
        []
      end
    end

    def required_keys_for(backend)
      return {} if backend == BACKEND_STANDALONE

      REQUIRED_KEYS
    end

    def validate_runtime_backend!(config)
      return if BACKENDS.include?(config.runtime_backend)

      raise InvalidEnvironmentError, "DEVOPSELLENCE_RUNTIME_BACKEND must be #{BACKENDS.join(' or ')}"
    end

    def validate_workload_identity_resource_names!(config)
      return if config.runtime_backend == BACKEND_STANDALONE

      pool = config.workload_identity_pool
      provider = config.workload_identity_provider

      unless pool.match?(%r{\Aprojects/\d+/locations/global/workloadIdentityPools/[a-z0-9-]+\z})
        raise InvalidEnvironmentError, "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_POOL must be a full workload identity pool resource name"
      end

      unless provider.match?(%r{\A#{Regexp.escape(pool)}/providers/[a-z0-9-]+\z})
        raise InvalidEnvironmentError, "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_PROVIDER must be a full workload identity provider resource name"
      end
    end

    def validate_public_base_url!(config)
      return unless config.runtime_backend == BACKEND_STANDALONE
      return if config.public_base_url.present?

      raise InvalidEnvironmentError, "DEVOPSELLENCE_PUBLIC_BASE_URL is required for standalone runtime backend"
    end

    def validate_public_base_url_scheme!(config)
      url = config.public_base_url.to_s
      return if url.empty?
      return if url.start_with?("https://")
      return if defined?(Rails) && Rails.env.development?

      raise InvalidEnvironmentError, "DEVOPSELLENCE_PUBLIC_BASE_URL must use https:// in production"
    end

    def validate_ingress_backend!(config)
      backend = config.ingress_backend.to_s
      return if %w[cloudflare local].include?(backend)

      raise InvalidEnvironmentError, "DEVOPSELLENCE_INGRESS_BACKEND must be cloudflare or local"
    end
  end
end
