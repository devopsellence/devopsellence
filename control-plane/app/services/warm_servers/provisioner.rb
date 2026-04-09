# frozen_string_literal: true

module WarmServers
  class Provisioner
    Error = Class.new(StandardError)

    def initialize(managed_provisioner: nil, registration_waiter: nil, provider_resolver: nil, on_progress: nil, wait_for_registration: true)
      @managed_provisioner = managed_provisioner
      @registration_waiter = registration_waiter
      @provider_resolver = provider_resolver || ->(slug) { ManagedNodes::Providers::Resolver.resolve(slug) }
      @on_progress = on_progress
      @wait_for_registration = wait_for_registration
    end

    def call
      node_name = provisioner.generate_node_name(prefix: "devopsellence-warm")
      Rails.logger.info("[warm_servers/provisioner] provisioning warm server name=#{node_name}")
      provision_result = provisioner.call(node_name:)
      if !wait_for_registration
        Rails.logger.info("[warm_servers/provisioner] server created; registration pending name=#{node_name} bootstrap_token=#{provision_result.bootstrap_token.id}")
        return provision_result.bootstrap_token
      end

      Rails.logger.info("[warm_servers/provisioner] waiting for registration name=#{node_name}")
      on_progress&.call("waiting for managed node registration")
      node = wait_for_node(provision_result.bootstrap_token)
      Rails.logger.info("[warm_servers/provisioner] warm server ready node_id=#{node.id} name=#{node.name}")
      node
    rescue StandardError => error
      cleanup_failed_provision!(provision_result, error)
      raise Error, error.message
    end

    private

    def runtime
      Devopsellence::RuntimeConfig.current
    end

    attr_reader :on_progress, :wait_for_registration

    def provisioner
      @managed_provisioner ||= ManagedNodes::Provisioner.new(
        organization: nil,
        provider_slug: runtime.managed_default_provider,
        region: runtime.managed_default_region,
        size_slug: runtime.managed_default_size_slug,
        base_url: runtime.public_base_url,
        pool_candidates: runtime.managed_pool_candidates
      )
    end

    def wait_for_node(bootstrap_token)
      return @registration_waiter.call(bootstrap_token) if @registration_waiter.respond_to?(:call)

      ManagedNodes::WaitForRegistration.new(
        bootstrap_token: bootstrap_token,
        issuer: runtime.public_base_url
      ).call
    end

    def cleanup_failed_provision!(provision_result, error)
      return unless provision_result

      bootstrap_token = provision_result.bootstrap_token
      if bootstrap_token&.provider_server_id.present?
        provider = @provider_resolver.call(bootstrap_token.managed_provider)
        provider.delete_server(provider_server_id: bootstrap_token.provider_server_id)
      end
      bootstrap_token&.update!(consumed_at: Time.current) if bootstrap_token&.persisted? && bootstrap_token.consumed_at.nil?
    rescue StandardError => cleanup_error
      Rails.logger.warn("[warm_servers/provisioner] cleanup failed after registration error original_error=#{error.message} cleanup_error=#{cleanup_error.message}")
    end
  end
end
