# frozen_string_literal: true

require "test_helper"

module CliReleases
  class FetcherTest < ActiveSupport::TestCase
    test "fetch builds a github release asset url" do
      artifact = Fetcher.new.fetch(version: "v0.1.0", os: "darwin", arch: "arm64")

      assert_equal "https://github.com/devopsellence/devopsellence/releases/download/v0.1.0/cli-darwin-arm64", artifact.url
      assert_equal "devopsellence", artifact.filename
      assert_equal "cli-darwin-arm64", artifact.source_name
    end

    test "fetch_checksums builds a github release asset url" do
      artifact = Fetcher.new.fetch_checksums(version: "v0.1.0")

      assert_equal "https://github.com/devopsellence/devopsellence/releases/download/v0.1.0/cli-SHA256SUMS", artifact.url
      assert_equal "SHA256SUMS", artifact.filename
    end

    test "fetch rejects unsupported targets" do
      assert_raises(Fetcher::UnsupportedTargetError) do
        Fetcher.new.fetch(version: "v0.1.0", os: "linux", arch: "ppc64le")
      end
    end
  end
end
