# frozen_string_literal: true

module ManagedNodes
  class WaitForRegistration
    Error = Class.new(StandardError)

    def initialize(bootstrap_token:, issuer:, timeout_seconds: nil, poll_interval_seconds: 2)
      @bootstrap_token = bootstrap_token
      @issuer = issuer
      @timeout_seconds = timeout_seconds || Devopsellence::RuntimeConfig.current.managed_registration_timeout_seconds.to_i
      @poll_interval_seconds = poll_interval_seconds
    end

    def call
      deadline = Time.current + timeout_seconds
      Rails.logger.info("[managed_nodes/wait_for_registration] waiting for node registration bootstrap_token=#{bootstrap_token_reference} timeout=#{timeout_seconds}s")

      loop do
        bootstrap_token.reload
        if bootstrap_token.node
          Rails.logger.info("[managed_nodes/wait_for_registration] node registered node_id=#{bootstrap_token.node.id} name=#{bootstrap_token.node.name}")
          return hydrate_node!(bootstrap_token.node)
        end

        raise Error, "managed node registration timed out after #{timeout_seconds}s" if Time.current >= deadline

        sleep poll_interval_seconds
      end
    end

    private

    attr_reader :bootstrap_token, :issuer, :timeout_seconds, :poll_interval_seconds

    def bootstrap_token_reference
      bootstrap_token.token_digest.to_s.first(8).presence || bootstrap_token.id || "unknown"
    end

    def hydrate_node!(node)
      attributes = {
        managed: true,
        managed_provider: bootstrap_token.managed_provider,
        managed_region: bootstrap_token.managed_region,
        managed_size_slug: bootstrap_token.managed_size_slug,
        provider_server_id: bootstrap_token.provider_server_id,
        public_ip: bootstrap_token.public_ip,
        provisioning_status: Node::PROVISIONING_READY
      }.compact
      node.update!(attributes)
      node
    rescue StandardError => error
      raise Error, error.message
    end
  end
end
