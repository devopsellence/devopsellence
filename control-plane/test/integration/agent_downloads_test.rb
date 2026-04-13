# frozen_string_literal: true

require "test_helper"

class AgentDownloadsTest < ActionDispatch::IntegrationTest
  FakeArtifact = Struct.new(:url, :filename, keyword_init: true)

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

  test "returns service unavailable when no version is requested and no stable version is configured" do
    with_env("DEVOPSELLENCE_AGENT_STABLE_VERSION" => nil) do
      get agent_download_path
    end

    assert_response :service_unavailable
    assert_includes response.body, "agent binary unavailable"
  end

  test "redirects explicit version to the configured release asset url" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(url: "https://github.com/devopsellence/devopsellence/releases/download/agent-v0.1.0/linux-arm64", filename: "devopsellence-agent"))

    with_agent_release_fetcher(fetcher) do
      get agent_download_path, params: { version: "v0.1.0", os: "linux", arch: "arm64" }
    end

    assert_response :redirect
    assert_equal "https://github.com/devopsellence/devopsellence/releases/download/agent-v0.1.0/linux-arm64", response.location
    assert_equal [{ version: "v0.1.0", os: "linux", arch: "arm64" }], fetcher.calls
    assert_includes response.headers["Cache-Control"], "public"
    assert_includes response.headers["Cache-Control"], "max-age=31536000"
    assert_includes response.headers["Cache-Control"], "immutable"
  end

  test "redirects unversioned requests to the stable version without downloading" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(url: "https://example.test/unused", filename: "devopsellence-agent"))

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
