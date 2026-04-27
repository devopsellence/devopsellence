# frozen_string_literal: true

require "securerandom"

module ManagedNodes
  class Provisioner
    CREATE_SERVER_ATTEMPTS = 3
    CREATE_SERVER_BACKOFF_SECONDS = [ 5, 15 ].freeze
    Error = Class.new(StandardError)
    Result = Struct.new(:bootstrap_token, :raw_bootstrap_token, :server, :node_name, keyword_init: true)

    def initialize(organization:, provider_slug:, region:, size_slug:, base_url:, provider: nil, pool_candidates: nil, provider_resolver: nil, sleeper: nil)
      @organization = organization
      @provider_slug = provider_slug
      @region = region
      @size_slug = size_slug
      @base_url = base_url
      @provider = provider
      @pool_candidates = Array(pool_candidates).presence
      @provider_resolver = provider_resolver || ->(slug) { Providers::Resolver.resolve(slug) }
      @sleeper = sleeper || ->(seconds) { sleep(seconds) }
    end

    def call(node_name:)
      raise Error, "configure DEVOPSELLENCE_PUBLIC_BASE_URL for managed node provisioning" if base_url.to_s.strip.empty?

      Rails.logger.info("[managed_nodes/provisioner] provisioning server name=#{node_name} provider=#{provider_slug} candidates=#{pool_candidates.map { |c| "#{c.fetch(:provider_slug)}/#{c.fetch(:region)}/#{c.fetch(:size_slug)}" }.join(", ")}")
      attempt_provision(node_name:)
    rescue StandardError => error
      raise Error, normalize_error_message(error)
    end

    def generate_node_name(prefix:)
      "#{prefix}-#{SecureRandom.hex(3)}"
    end

    private

    attr_reader :organization, :provider_slug, :region, :size_slug, :base_url, :sleeper, :provider_resolver

    def pool_candidates
      @pool_candidates || [ default_pool_candidate ]
    end

    def default_pool_candidate
      {
        provider_slug: provider_slug,
        region: region,
        size_slug: size_slug
      }
    end

    def provider
      @provider ||= Providers::Resolver.resolve(provider_slug)
    end

    def provider_for(candidate)
      return provider if candidate == default_pool_candidate

      provider_resolver.call(candidate.fetch(:provider_slug))
    end

    def attempt_provision(node_name:)
      placement_error = nil

      pool_candidates.each do |candidate|
        current_provider = provider_for(candidate)
        bootstrap, raw_bootstrap = issue_bootstrap(candidate)
        server = nil

        begin
          server = create_server_with_retry(
            provider: current_provider,
            name: node_name,
            region: candidate.fetch(:region),
            size_slug: candidate.fetch(:size_slug),
            user_data: BootstrapScript.new(
              node_name: node_name,
              bootstrap_token: raw_bootstrap,
              base_url: base_url
            ).render
          )

          bootstrap.update!(
            provider_server_id: server.id,
            public_ip: current_provider.public_ip(server)
          )

          Rails.logger.info("[managed_nodes/provisioner] server created name=#{node_name} provider=#{candidate.fetch(:provider_slug)} region=#{candidate.fetch(:region)} server_id=#{server.id} ip=#{current_provider.public_ip(server)}")
          return Result.new(
            bootstrap_token: bootstrap,
            raw_bootstrap_token: raw_bootstrap,
            server: server,
            node_name: node_name
          )
        rescue StandardError => error
          current_provider.delete_server(provider_server_id: server.id) if server&.id.present?
          bootstrap.update!(consumed_at: Time.current) if bootstrap.persisted? && bootstrap.consumed_at.nil?
          raise error unless retryable_placement_error?(error)

          Rails.logger.warn("[managed_nodes/provisioner] placement failed, trying next candidate provider=#{candidate.fetch(:provider_slug)} region=#{candidate.fetch(:region)}")
          placement_error = error
        end
      end

      raise placement_error if placement_error
      raise Error, "managed capacity temporarily unavailable"
    end

    def issue_bootstrap(candidate)
      NodeBootstrapToken.issue!(
        organization: organization,
        purpose: NodeBootstrapToken::PURPOSE_MANAGED_POOL_NODE,
        managed_provider: candidate.fetch(:provider_slug),
        managed_region: candidate.fetch(:region),
        managed_size_slug: candidate.fetch(:size_slug)
      )
    end

    def create_server_with_retry(provider:, name:, region:, size_slug:, user_data:)
      attempts = 0

      begin
        attempts += 1
        Rails.logger.info("[managed_nodes/provisioner] creating server attempt=#{attempts}/#{CREATE_SERVER_ATTEMPTS} name=#{name} region=#{region} size=#{size_slug}") if attempts > 1
        provider.create_server(
          name: name,
          region: region,
          size_slug: size_slug,
          user_data: user_data
        )
      rescue StandardError => error
        raise error unless retryable_placement_error?(error)
        raise error if attempts >= CREATE_SERVER_ATTEMPTS

        sleeper.call(CREATE_SERVER_BACKOFF_SECONDS.fetch(attempts - 1, CREATE_SERVER_BACKOFF_SECONDS.last))
        retry
      end
    end

    def retryable_placement_error?(error)
      message = error.message.to_s
      message.include?('"code": "resource_unavailable"') &&
        message.include?('"message": "error during placement"')
    end

    def normalize_error_message(error)
      return error.message unless retryable_placement_error?(error)

      pools = pool_candidates.map { |candidate| "#{candidate.fetch(:region)}/#{candidate.fetch(:size_slug)}" }.join(", ")
      "No managed server capacity is available in #{pools} right now. Retry in a few minutes, or use your own VM/server with `devopsellence init --mode solo`."
    end
  end
end
