# frozen_string_literal: true

require "erb"

module AgentReleases
  class Fetcher
    Error = Class.new(StandardError)
    NotConfiguredError = Class.new(Error)
    UnsupportedTargetError = Class.new(Error)
    Artifact = Struct.new(:url, :filename, :source_name, keyword_init: true)

    DEFAULT_BASE_DOWNLOAD_URL = "https://github.com/devopsellence/devopsellence/releases/download"
    DEFAULT_ASSET_PREFIX = "agent"
    SUPPORTED_OSES = %w[linux darwin].freeze
    SUPPORTED_ARCHES = %w[amd64 arm64].freeze

    def self.build
      new
    end

    def initialize(base_download_url: DEFAULT_BASE_DOWNLOAD_URL, asset_prefix: DEFAULT_ASSET_PREFIX)
      @base_download_url = base_download_url.to_s.delete_suffix("/")
      @asset_prefix = asset_prefix.to_s
    end

    def fetch(version:, os:, arch:)
      validate_target!(os:, arch:)

      source_name = artifact_name(os:, arch:)
      Artifact.new(
        url: download_url(version:, source_name:),
        filename: "devopsellence-agent",
        source_name:
      )
    end

    def fetch_checksums(version:)
      Artifact.new(
        url: download_url(version:, source_name: "#{asset_prefix}-SHA256SUMS"),
        filename: "SHA256SUMS",
        source_name: "#{asset_prefix}-SHA256SUMS"
      )
    end

    private

    attr_reader :asset_prefix, :base_download_url

    def validate_target!(os:, arch:)
      return if SUPPORTED_OSES.include?(os) && SUPPORTED_ARCHES.include?(arch)

      raise UnsupportedTargetError, "unsupported target #{os}/#{arch}"
    end

    def artifact_name(os:, arch:)
      "#{asset_prefix}-#{os}-#{arch}"
    end

    def download_url(version:, source_name:)
      encoded_tag = ERB::Util.url_encode(version)
      encoded_source_name = ERB::Util.url_encode(source_name)
      "#{base_download_url}/#{encoded_tag}/#{encoded_source_name}"
    end
  end
end
