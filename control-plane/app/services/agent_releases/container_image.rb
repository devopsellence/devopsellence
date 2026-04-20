# frozen_string_literal: true

module AgentReleases
  class ContainerImage
    def self.metadata
      new.metadata
    end

    def initialize(
      image_reference: Devopsellence::RuntimeConfig.current.agent_container_image,
      image_repository: Devopsellence::RuntimeConfig.current.agent_container_repository,
      stable_version: Devopsellence::RuntimeConfig.current.stable_version
    )
      @image_reference = image_reference.to_s.strip
      @image_repository = image_repository.to_s.strip
      @stable_version = stable_version.to_s.strip
    end

    def metadata
      payload = {
        reference: resolved_reference,
        version: stable_version.presence
      }.compact
      payload.presence
    end

    private

    attr_reader :image_reference, :image_repository, :stable_version

    def resolved_reference
      return image_reference if image_reference.present?
      return nil if image_repository.blank? || stable_version.blank?

      "#{image_repository}:#{stable_version}"
    end
  end
end
