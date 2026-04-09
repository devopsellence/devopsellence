# frozen_string_literal: true

require "erb"
require "json"
require "uri"

module AgentReleases
  class Fetcher
    Error = Class.new(StandardError)
    NotConfiguredError = Class.new(Error)
    NotFoundError = Class.new(Error)
    UnsupportedTargetError = Class.new(Error)
    Artifact = Struct.new(:body, :filename, :source_name, keyword_init: true)

    SUPPORTED_OSES = %w[linux darwin].freeze
    SUPPORTED_ARCHES = %w[amd64 arm64].freeze

    def self.build
      runtime = Devopsellence::RuntimeConfig.current
      new(
        project_id: runtime.agent_release_project_id,
        location: runtime.agent_release_region,
        repository: runtime.agent_release_repository,
        package_name: runtime.agent_release_package,
        client: Gcp::RestClient.new
      )
    end

    def initialize(project_id:, location:, repository:, package_name: nil, client:)
      @project_id = project_id.to_s.strip
      @location = location.to_s.strip
      @repository = repository.to_s.strip
      @package_name = package_name.to_s.strip.presence || "devopsellence-agent"
      @client = client
    end

    def fetch(version:, os:, arch:)
      ensure_configured!
      validate_target!(os:, arch:)

      artifact_name = artifact_name(version:, os:, arch:)
      source_name = resolve_source_name(version:, artifact_name:)
      response = client.get(download_uri(source_name))
      raise Error, "artifact download failed (#{response.code})" unless response.code.to_i.between?(200, 299)

      Artifact.new(body: response.body, filename: "devopsellence-agent", source_name: source_name)
    end

    def fetch_checksums(version:)
      ensure_configured!

      source_name = resolve_source_name(version:, artifact_name: "SHA256SUMS")
      response = client.get(download_uri(source_name))
      raise Error, "artifact download failed (#{response.code})" unless response.code.to_i.between?(200, 299)

      Artifact.new(body: response.body, filename: "SHA256SUMS", source_name: source_name)
    end

    private

    attr_reader :project_id, :location, :repository, :package_name, :client

    def ensure_configured!
      return if [ project_id, location, repository ].all?(&:present?)

      raise NotConfiguredError, "configure DEVOPSELLENCE_AGENT_RELEASE_PROJECT_ID, DEVOPSELLENCE_AGENT_RELEASE_REGION, and DEVOPSELLENCE_AGENT_RELEASE_REPOSITORY"
    end

    def validate_target!(os:, arch:)
      return if SUPPORTED_OSES.include?(os) && SUPPORTED_ARCHES.include?(arch)

      raise UnsupportedTargetError, "unsupported target #{os}/#{arch}"
    end

    def artifact_name(version:, os:, arch:)
      "#{os}-#{arch}"
    end

    def resolve_source_name(version:, artifact_name:)
      response = client.get(files_uri(version:))
      raise Error, "artifact list failed (#{response.code})" unless response.code.to_i.between?(200, 299)

      files = JSON.parse(response.body).fetch("files", [])
      match = files.find do |entry|
        file_name = entry["name"].to_s.split("/files/", 2).last
        file_name.end_with?(":#{artifact_name}") || file_name == artifact_name
      end
      return match.fetch("name").split("/files/", 2).last if match

      raise NotFoundError, "artifact not found for #{version}"
    end

    def files_uri(version:)
      parent = "projects/#{project_id}/locations/#{location}/repositories/#{repository}"
      owner = "#{parent}/packages/#{package_name}/versions/#{version}"
      uri = URI.parse("#{Gcp::Endpoints.artifact_registry_base}/#{parent}/files")
      uri.query = URI.encode_www_form(filter: %(owner="#{owner}"))
      uri.to_s
    end

    def download_uri(source_name)
      encoded_name = ERB::Util.url_encode(source_name)
      "#{Gcp::Endpoints.artifact_registry_download_base}/projects/#{project_id}/locations/#{location}/repositories/#{repository}/files/#{encoded_name}:download?alt=media"
    end
  end
end
