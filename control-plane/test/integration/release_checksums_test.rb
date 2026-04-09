# frozen_string_literal: true

require "test_helper"

class ReleaseChecksumsTest < ActionDispatch::IntegrationTest
  FakeArtifact = Struct.new(:body, :filename, keyword_init: true)

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
    fetcher = FakeFetcher.new(result: FakeArtifact.new(body: "checksums", filename: "SHA256SUMS"))

    with_env("DEVOPSELLENCE_CLI_STABLE_VERSION" => "v1.2.3") do
      with_cli_release_fetcher(fetcher) do
        get cli_checksums_path
      end
    end

    assert_response :redirect
    assert_equal "http://www.example.com/cli/checksums?version=v1.2.3", response.location
    assert_empty fetcher.calls
  end

  test "cli checksums cache explicit version responses" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(body: "checksums", filename: "SHA256SUMS"))

    with_cli_release_fetcher(fetcher) do
      get cli_checksums_path, params: { version: "v0.1.0" }
    end

    assert_response :success
    assert_equal "checksums", response.body
    assert_equal [{ version: "v0.1.0" }], fetcher.calls
    assert_equal "text/plain", response.media_type
    assert_includes response.headers["Cache-Control"], "public"
    assert_includes response.headers["Cache-Control"], "max-age=31536000"
    assert_includes response.headers["Cache-Control"], "immutable"
  end

  test "agent checksums redirect unversioned requests to the stable version" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(body: "checksums", filename: "SHA256SUMS"))

    with_env("DEVOPSELLENCE_AGENT_STABLE_VERSION" => "v2.3.4") do
      with_agent_release_fetcher(fetcher) do
        get agent_checksums_path
      end
    end

    assert_response :redirect
    assert_equal "http://www.example.com/agent/checksums?version=v2.3.4", response.location
    assert_empty fetcher.calls
  end

  test "agent checksums cache explicit version responses" do
    fetcher = FakeFetcher.new(result: FakeArtifact.new(body: "checksums", filename: "SHA256SUMS"))

    with_agent_release_fetcher(fetcher) do
      get agent_checksums_path, params: { version: "v0.1.0" }
    end

    assert_response :success
    assert_equal "checksums", response.body
    assert_equal [{ version: "v0.1.0" }], fetcher.calls
    assert_equal "text/plain", response.media_type
    assert_includes response.headers["Cache-Control"], "public"
    assert_includes response.headers["Cache-Control"], "max-age=31536000"
    assert_includes response.headers["Cache-Control"], "immutable"
  end
end
