# frozen_string_literal: true

module Cli
  class RegistryPushAuthIssuer
    Error = Class.new(StandardError)

    def initialize(organization:, broker: nil)
      @organization = organization
      @broker = broker || Runtime::Broker.current
    end

    def call
      push_auth = broker.issue_gar_push_auth!(organization:)
      {
        registry_host: push_auth.registry_host,
        repository_path: push_auth.gar_repository_path,
        docker_username: push_auth.docker_username,
        docker_password: push_auth.docker_password,
        expires_in: push_auth.expires_in
      }
    end

    private

    attr_reader :organization, :broker
  end
end
