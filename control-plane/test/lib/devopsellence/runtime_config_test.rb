# frozen_string_literal: true

require "test_helper"

module Devopsellence
  class RuntimeConfigTest < ActiveSupport::TestCase
    test "defaults ash managed pool to hil fallback" do
      config = RuntimeConfig.load!(env: ENV.to_h)

      assert_equal "hetzner", config.managed_default_provider
      assert_equal "ash", config.managed_default_region
      assert_equal "cpx11", config.managed_default_size_slug
      assert_equal [
        { provider_slug: "hetzner", region: "ash", size_slug: "cpx11" },
        { provider_slug: "hetzner", region: "hil", size_slug: "cpx11" }
      ], config.managed_pool_candidates
      assert_equal "180", config.managed_registration_timeout_seconds
      assert_equal "60", config.managed_lease_minutes
      assert_equal "1", config.managed_pool_target
      assert_equal "5", config.managed_max_total
      assert_equal "1", config.organization_bundle_target
      assert_equal "1", config.environment_bundle_target
      assert_equal "1", config.node_bundle_target
    end

    test "managed pool candidates include hardcoded fallback candidate" do
      config = RuntimeConfig.load!(env: ENV.to_h)

      assert_includes config.managed_pool_candidates, { provider_slug: "hetzner", region: "hil", size_slug: "cpx11" }
    end

    test "app config defaults stay in runtime config" do
      with_env("MAIL_FROM_ADDRESS" => nil, "DEVOPSELLENCE_CLOUDFLARE_ENVOY_ORIGIN" => nil, "DEVOPSELLENCE_ACTIVITY_NOTIFICATION_TO" => nil) do
        config = RuntimeConfig.load!(env: ENV.to_h)

        assert_equal "", config.cloudflare_account_id
        assert_equal "", config.cloudflare_zone_id
        assert_equal "devopsellence.io", config.cloudflare_zone_name
        assert_equal "http://devopsellence-envoy:8000", config.cloudflare_envoy_origin
        assert_equal "devopsellence-agent", config.agent_release_package
        assert_equal "devopsellence-cli", config.cli_release_package
        assert_equal "noreply@example.com", config.mail_from_address
        assert_equal "", config.activity_notification_to
      end
    end

    test "development defaults acme directory to letsencrypt staging" do
      with_env("DEVOPSELLENCE_ACME_DIRECTORY_URL" => nil) do
        config = RuntimeConfig.load_current!(env: ENV.to_h, rails_env: "development")

        assert_equal RuntimeConfig::DEFAULT_ACME_STAGING_DIRECTORY_URL, config.acme_directory_url
      end
    end

    test "development boots without local gcp runtime env" do
      env = ENV.to_h.except(
        "DEVOPSELLENCE_RUNTIME_BACKEND",
        "DEVOPSELLENCE_PUBLIC_BASE_URL",
        "DEVOPSELLENCE_INGRESS_BACKEND",
        "DEVOPSELLENCE_LOCAL_INGRESS_PUBLIC_URL",
        "DEVOPSELLENCE_DEFAULT_GCP_PROJECT_ID",
        "DEVOPSELLENCE_DEFAULT_GCP_PROJECT_NUMBER",
        "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_POOL",
        "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_PROVIDER",
        "DEVOPSELLENCE_DEFAULT_GAR_REGION",
        "DEVOPSELLENCE_GCS_BUCKET_PREFIX",
        "DEVOPSELLENCE_MANAGED_POOL_TARGET",
        "DEVOPSELLENCE_ORGANIZATION_BUNDLE_TARGET",
        "DEVOPSELLENCE_ENVIRONMENT_BUNDLE_TARGET",
        "DEVOPSELLENCE_NODE_BUNDLE_TARGET",
        "DEVOPSELLENCE_MANAGED_MAX_TOTAL"
      )

      Rails.stubs(:env).returns(ActiveSupport::StringInquirer.new("development"))

      config = RuntimeConfig.load_current!(env:, rails_env: "development")

      assert_equal "standalone", config.runtime_backend
      assert_equal "http://localhost:3000", config.public_base_url
      assert_equal "local", config.ingress_backend
      assert_equal "http://localhost:3000", config.local_ingress_public_url
      assert_equal "0", config.managed_pool_target
      assert_equal "0", config.organization_bundle_target
      assert_equal "0", config.environment_bundle_target
      assert_equal "0", config.node_bundle_target
      assert_equal "0", config.managed_max_total
    end

    test "development preserves explicit acme directory override" do
      with_env("DEVOPSELLENCE_ACME_DIRECTORY_URL" => "https://example.test/acme") do
        config = RuntimeConfig.load_current!(env: ENV.to_h, rails_env: "development")

        assert_equal "https://example.test/acme", config.acme_directory_url
      end
    end

    test "accepts local ingress backend" do
      with_env(
        "DEVOPSELLENCE_INGRESS_BACKEND" => "local",
        "DEVOPSELLENCE_LOCAL_INGRESS_PUBLIC_URL" => "http://127.0.0.1:18080"
      ) do
        config = RuntimeConfig.load!(env: ENV.to_h)

        assert_equal "local", config.ingress_backend
        assert_equal "http://127.0.0.1:18080", config.local_ingress_public_url
      end
    end

    test "rejects unknown ingress backend" do
      error = assert_raises(RuntimeConfig::InvalidEnvironmentError) do
        with_env("DEVOPSELLENCE_INGRESS_BACKEND" => "bogus") do
        end
      end

      assert_match(/DEVOPSELLENCE_INGRESS_BACKEND/, error.message)
    end

    test "standalone backend skips gcp env requirements" do
      with_env(
        "DEVOPSELLENCE_RUNTIME_BACKEND" => "standalone",
        "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://control.example.test",
        "DEVOPSELLENCE_DEFAULT_GCP_PROJECT_ID" => nil,
        "DEVOPSELLENCE_DEFAULT_GCP_PROJECT_NUMBER" => nil,
        "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_POOL" => nil,
        "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_PROVIDER" => nil,
        "DEVOPSELLENCE_DEFAULT_GAR_REGION" => nil,
        "DEVOPSELLENCE_GCS_BUCKET_PREFIX" => nil
      ) do
        config = RuntimeConfig.load!(env: ENV.to_h)

        assert_equal "standalone", config.runtime_backend
        assert_equal "https://control.example.test", config.public_base_url
      end
    end

    test "rejects http public base url in non-local environments" do
      error = assert_raises(RuntimeConfig::InvalidEnvironmentError) do
        RuntimeConfig.load!(env: ENV.to_h.merge(
          "DEVOPSELLENCE_PUBLIC_BASE_URL" => "http://control.example.test"
        ))
      end

      assert_match(/https/, error.message)
    end
  end
end
