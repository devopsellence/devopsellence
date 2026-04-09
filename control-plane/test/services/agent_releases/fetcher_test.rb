# frozen_string_literal: true

require "cgi"
require "test_helper"

module AgentReleases
  class FetcherTest < ActiveSupport::TestCase
    FakeResponse = Struct.new(:code, :body, keyword_init: true)

    class FakeClient
      attr_reader :uris

      def initialize(list_response:, download_response:)
        @list_response = list_response
        @download_response = download_response
        @uris = []
      end

      def get(uri)
        @uris << uri
        return @list_response if uri.include?("/files?")

        @download_response
      end
    end

    test "fetch resolves artifact file and downloads bytes" do
      client = FakeClient.new(
        list_response: FakeResponse.new(
          code: 200,
          body: JSON.generate(
            files: [
              {
                name: "projects/proj/locations/us-central1/repositories/releases/files/devopsellence-agent:v0.1.0:linux-amd64"
              }
            ]
          )
        ),
        download_response: FakeResponse.new(code: 200, body: "binary")
      )

      artifact = Fetcher.new(
        project_id: "proj",
        location: "us-central1",
        repository: "releases",
        package_name: "devopsellence-agent",
        client: client
      ).fetch(version: "v0.1.0", os: "linux", arch: "amd64")

      assert_equal "binary", artifact.body
      assert_equal "devopsellence-agent", artifact.filename
      assert_includes client.uris.first, CGI.escape(%(owner="projects/proj/locations/us-central1/repositories/releases/packages/devopsellence-agent/versions/v0.1.0"))
      assert_match(/download\/v1/, client.uris.last)
    end

    test "fetch rejects unsupported targets" do
      fetcher = Fetcher.new(
        project_id: "proj",
        location: "us-central1",
        repository: "releases",
        package_name: "devopsellence-agent",
        client: Object.new
      )

      assert_raises(Fetcher::UnsupportedTargetError) do
        fetcher.fetch(version: "v0.1.0", os: "linux", arch: "ppc64le")
      end
    end

    test "fetch raises not found when version file is missing" do
      client = FakeClient.new(
        list_response: FakeResponse.new(code: 200, body: JSON.generate(files: [])),
        download_response: FakeResponse.new(code: 200, body: "unused")
      )

      fetcher = Fetcher.new(
        project_id: "proj",
        location: "us-central1",
        repository: "releases",
        package_name: "devopsellence-agent",
        client: client
      )

      assert_raises(Fetcher::NotFoundError) do
        fetcher.fetch(version: "v0.1.0", os: "linux", arch: "amd64")
      end
    end
  end
end
