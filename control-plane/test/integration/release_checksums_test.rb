# frozen_string_literal: true

require "test_helper"

class ReleaseChecksumsTest < ActionDispatch::IntegrationTest
  FakeArtifact = Struct.new(:url, :filename, keyword_init: true)

  class FakeFetcher
    attr_reader :calls

    def initialize(result: nil)
      @result = result
      @calls = []
    end

    def fetch_checksums(version:)
      @calls << { version: version }
      @result
    end
  end

  test "cli checksums redirect unversioned requests to the stable version" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(url: "https://example.test/unused", filename: "SHA256SUMS"))

    with_env("DEVOPSELLENCE_STABLE_VERSION" => "v1.2.3") do
      with_cli_release_fetcher(fetcher) do
        get cli_checksums_path
      end
    end

    assert_response :redirect
    assert_equal "http://www.example.com/cli/checksums?version=v1.2.3", response.location
    assert_empty fetcher.calls
  end

  test "cli checksums redirect explicit version requests to the release asset url" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(url: "https://github.com/devopsellence/devopsellence/releases/download/v0.1.0/cli-SHA256SUMS", filename: "SHA256SUMS"))

    with_cli_release_fetcher(fetcher) do
      get cli_checksums_path, params: { version: "v0.1.0" }
    end

    assert_response :redirect
    assert_equal "https://github.com/devopsellence/devopsellence/releases/download/v0.1.0/cli-SHA256SUMS", response.location
    assert_equal [{ version: "v0.1.0" }], fetcher.calls
    assert_includes response.headers["Cache-Control"], "public"
    assert_includes response.headers["Cache-Control"], "max-age=31536000"
    assert_includes response.headers["Cache-Control"], "immutable"
  end

  test "agent checksums redirect unversioned requests to the stable version" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(url: "https://example.test/unused", filename: "SHA256SUMS"))

    with_env("DEVOPSELLENCE_STABLE_VERSION" => "v2.3.4") do
      with_agent_release_fetcher(fetcher) do
        get agent_checksums_path
      end
    end

    assert_response :redirect
    assert_equal "http://www.example.com/agent/checksums?version=v2.3.4", response.location
    assert_empty fetcher.calls
  end

  test "agent checksums redirect explicit version requests to the release asset url" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(url: "https://github.com/devopsellence/devopsellence/releases/download/v0.1.0/agent-SHA256SUMS", filename: "SHA256SUMS"))

    with_agent_release_fetcher(fetcher) do
      get agent_checksums_path, params: { version: "v0.1.0" }
    end

    assert_response :redirect
    assert_equal "https://github.com/devopsellence/devopsellence/releases/download/v0.1.0/agent-SHA256SUMS", response.location
    assert_equal [{ version: "v0.1.0" }], fetcher.calls
    assert_includes response.headers["Cache-Control"], "public"
    assert_includes response.headers["Cache-Control"], "max-age=31536000"
    assert_includes response.headers["Cache-Control"], "immutable"
  end
end
