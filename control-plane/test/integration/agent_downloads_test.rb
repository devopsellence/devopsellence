# frozen_string_literal: true

require "test_helper"

class AgentDownloadsTest < ActionDispatch::IntegrationTest
  FakeArtifact = Struct.new(:body, :filename, keyword_init: true)

  class FakeFetcher
    attr_reader :calls

    def initialize(result: nil, error: nil)
      @result = result
      @error = error
      @calls = []
    end

    def fetch(version:, os:, arch:)
      @calls << { version: version, os: os, arch: arch }
      raise @error if @error

      @result
    end
  end

  test "returns service unavailable when no version or release config is configured" do
    with_env(
      "DEVOPSELLENCE_AGENT_STABLE_VERSION" => nil,
      "DEVOPSELLENCE_AGENT_RELEASE_PROJECT_ID" => nil,
      "DEVOPSELLENCE_AGENT_RELEASE_REGION" => nil,
      "DEVOPSELLENCE_AGENT_RELEASE_REPOSITORY" => nil
    ) do
      get agent_download_path
    end

    assert_response :service_unavailable
    assert_includes response.body, "agent binary unavailable"
  end

  test "downloads explicit version from the configured fetcher" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(body: "binary", filename: "devopsellence-agent"))

    with_agent_release_fetcher(fetcher) do
      get agent_download_path, params: { version: "v0.1.0", os: "linux", arch: "arm64" }
    end

    assert_response :success
    assert_equal "binary", response.body
    assert_equal [{ version: "v0.1.0", os: "linux", arch: "arm64" }], fetcher.calls
    assert_equal "application/octet-stream", response.media_type
    assert_match(/attachment/, response.headers["Content-Disposition"])
    assert_includes response.headers["Cache-Control"], "public"
    assert_includes response.headers["Cache-Control"], "max-age=31536000"
    assert_includes response.headers["Cache-Control"], "immutable"
  end

  test "redirects unversioned requests to the stable version without downloading" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(body: "binary", filename: "devopsellence-agent"))

    with_env("DEVOPSELLENCE_AGENT_STABLE_VERSION" => "v1.2.3") do
      with_agent_release_fetcher(fetcher) do
        get agent_download_path
      end
    end

    assert_response :redirect
    assert_equal "http://www.example.com/agent/download?version=v1.2.3", response.location
    assert_empty fetcher.calls
  end

  test "returns unprocessable entity for unsupported targets" do
    fetcher = FakeFetcher.new(error: AgentReleases::Fetcher::UnsupportedTargetError.new("unsupported target linux/s390x"))

    with_agent_release_fetcher(fetcher) do
      get agent_download_path, params: { version: "v0.1.0", os: "linux", arch: "s390x" }
    end

    assert_response :unprocessable_entity
    assert_includes response.body, "unsupported target"
  end
end
