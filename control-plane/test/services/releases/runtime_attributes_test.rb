# frozen_string_literal: true

require "test_helper"

module Releases
  class RuntimeAttributesTest < ActiveSupport::TestCase
    test "raises invalid payload for non-string JSON scalar params" do
      error = assert_raises(RuntimeAttributes::InvalidPayload) do
        RuntimeAttributes.new(
          params: {
            git_sha: "a" * 40,
            image_repository: "api",
            image_digest: "sha256:#{"b" * 64}",
            services: 42
          }
        ).to_h
      end

      assert_equal "services must be valid JSON", error.message
    end

    test "raises invalid payload when service kind is blank" do
      error = assert_raises(RuntimeAttributes::InvalidPayload) do
        RuntimeAttributes.new(
          params: {
            git_sha: "a" * 40,
            image_repository: "api",
            image_digest: "sha256:#{"b" * 64}",
            services: {
              web: {
                kind: "   "
              }
            }
          }
        ).to_h
      end

      assert_equal "services.web.kind must be present", error.message
    end

    test "rejects deprecated entrypoint fields" do
      error = assert_raises(RuntimeAttributes::InvalidPayload) do
        RuntimeAttributes.new(
          params: {
            git_sha: "a" * 40,
            image_repository: "api",
            image_digest: "sha256:#{"b" * 64}",
            services: {
              web: {
                kind: "web",
                entrypoint: ["/app"],
                ports: [{ name: "http", port: 3000 }],
                healthcheck: { path: "/up", port: 3000 }
              }
            }
          }
        ).to_h
      end

      assert_equal "services.web.entrypoint is no longer supported; use command or args", error.message
    end

    test "rejects non-string argv entries" do
      error = assert_raises(RuntimeAttributes::InvalidPayload) do
        RuntimeAttributes.new(
          params: {
            git_sha: "a" * 40,
            image_repository: "api",
            image_digest: "sha256:#{"b" * 64}",
            services: {
              web: {
                kind: "web",
                command: ["/app", 123],
                ports: [{ name: "http", port: 3000 }],
                healthcheck: { path: "/up", port: 3000 }
              }
            }
          }
        ).to_h
      end

      assert_equal "services.web.command[1] must be a string", error.message
    end

    test "preserves argv arrays for service command and release args" do
      attrs = RuntimeAttributes.new(
        params: {
          git_sha: "a" * 40,
          image_repository: "api",
          image_digest: "sha256:#{"b" * 64}",
          services: {
            web: {
              kind: "web",
              command: ["/app"],
              args: ["web"],
              ports: [{ name: "http", port: 3000 }],
              healthcheck: { path: "/up", port: 3000 }
            }
          },
          tasks: {
            release: {
              service: "web",
              args: ["release"]
            }
          }
        }
      ).to_h

      runtime = JSON.parse(attrs.fetch(:runtime_json))
      assert_equal ["/app"], runtime.dig("services", "web", "command")
      assert_equal ["web"], runtime.dig("services", "web", "args")
      assert_equal ["release"], runtime.dig("tasks", "release", "args")
    end
  end
end
