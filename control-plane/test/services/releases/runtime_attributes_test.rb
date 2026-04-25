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

    test "rejects explicit service kind fields" do
      error = assert_raises(RuntimeAttributes::InvalidPayload) do
        RuntimeAttributes.new(
          params: {
            git_sha: "a" * 40,
            image_repository: "api",
            image_digest: "sha256:#{"b" * 64}",
            services: {
              web: {
                kind: "web"
              }
            }
          }
        ).to_h
      end

      assert_equal "services.web.kind is no longer supported", error.message
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
          },
          ingress: {
            hosts: ["app.example.test", "www.example.test"],
            rules: [
              {
                match: { host: "app.example.test", path_prefix: "/" },
                target: { service: "web", port: "http" }
              },
              {
                match: { host: "www.example.test", path_prefix: "/" },
                target: { service: "web", port: "http" }
              }
            ],
            tls: {
              mode: "manual"
            }
          }
        }
      ).to_h

      runtime = JSON.parse(attrs.fetch(:runtime_json))
      assert_equal ["/app"], runtime.dig("services", "web", "command")
      assert_equal ["web"], runtime.dig("services", "web", "args")
      assert_equal ["release"], runtime.dig("tasks", "release", "args")
      assert_equal ["app.example.test", "www.example.test"], runtime.dig("ingress", "hosts")
      assert_equal "web", runtime.dig("ingress", "rules", 0, "target", "service")
      assert_equal "manual", runtime.dig("ingress", "tls", "mode")
    end

    test "rejects non-boolean ingress redirect_http" do
      error = assert_raises(RuntimeAttributes::InvalidPayload) do
        RuntimeAttributes.new(
          params: {
            git_sha: "a" * 40,
            image_repository: "api",
            image_digest: "sha256:#{"b" * 64}",
            services: {
              web: {
                ports: [{ name: "http", port: 3000 }],
                healthcheck: { path: "/up", port: 3000 }
              }
            },
            ingress: {
              hosts: ["app.example.test"],
              rules: [
                {
                  match: { host: "app.example.test", path_prefix: "/" },
                  target: { service: "web", port: "http" }
                }
              ],
              redirect_http: "yes"
            }
          }
        ).to_h
      end

      assert_equal "ingress.redirect_http must be a boolean", error.message
    end

    test "preserves explicit false ingress redirect_http" do
      attrs = RuntimeAttributes.new(
        params: {
          git_sha: "a" * 40,
          image_repository: "api",
          image_digest: "sha256:#{"b" * 64}",
          services: {
            web: {
              ports: [{ name: "http", port: 3000 }],
              healthcheck: { path: "/up", port: 3000 }
            }
            },
            ingress: {
              hosts: ["app.example.test"],
              rules: [
                {
                  match: { host: "app.example.test", path_prefix: "/" },
                  target: { service: "web", port: "http" }
                }
              ],
              redirect_http: false
            }
          }
      ).to_h

      runtime = JSON.parse(attrs.fetch(:runtime_json))
      assert_equal false, runtime.dig("ingress", "redirect_http")
    end

    test "normalizes ingress hosts and rejects case-insensitive duplicates" do
      error = assert_raises(RuntimeAttributes::InvalidPayload) do
        RuntimeAttributes.new(
          params: {
            git_sha: "a" * 40,
            image_repository: "api",
            image_digest: "sha256:#{"b" * 64}",
            services: {
              web: {
                ports: [{ name: "http", port: 3000 }],
                healthcheck: { path: "/up", port: 3000 }
              }
            },
            ingress: {
              hosts: ["App.Example.Test", "app.example.test"],
              rules: [
                {
                  match: { host: "app.example.test", path_prefix: "/" },
                  target: { service: "web", port: "http" }
                }
              ]
            }
          }
        ).to_h
      end

      assert_equal "ingress.hosts must be unique", error.message

      attrs = RuntimeAttributes.new(
        params: {
          git_sha: "a" * 40,
          image_repository: "api",
          image_digest: "sha256:#{"b" * 64}",
          services: {
            web: {
              ports: [{ name: "http", port: 3000 }],
              healthcheck: { path: "/up", port: 3000 }
            }
            },
            ingress: {
              hosts: ["App.Example.Test", "WWW.Example.Test"],
              rules: [
                {
                  match: { host: "App.Example.Test", path_prefix: "/" },
                  target: { service: "web", port: "http" }
                },
                {
                  match: { host: "WWW.Example.Test", path_prefix: "/" },
                  target: { service: "web", port: "http" }
                }
              ]
            }
          }
      ).to_h

      runtime = JSON.parse(attrs.fetch(:runtime_json))
      assert_equal ["app.example.test", "www.example.test"], runtime.dig("ingress", "hosts")
      assert_equal "app.example.test", runtime.dig("ingress", "rules", 0, "match", "host")
      assert_equal "www.example.test", runtime.dig("ingress", "rules", 1, "match", "host")
    end

    test "preserves explicit ingress rules payload" do
      attrs = RuntimeAttributes.new(
        params: {
          git_sha: "a" * 40,
          image_repository: "api",
          image_digest: "sha256:#{"b" * 64}",
          services: {
            app: {
              ports: [{ name: "http", port: 3000 }],
              healthcheck: { path: "/up", port: 3000 }
            },
            api: {
              ports: [{ name: "metrics", port: 9090 }]
            }
          },
          ingress: {
            hosts: ["app.example.com"],
            rules: [
              {
                match: { host: "app.example.com", path_prefix: "/api" },
                target: { service: "api", port: "metrics" }
              },
              {
                match: { host: "app.example.com", path_prefix: "/" },
                target: { service: "app", port: "http" }
              }
            ]
          }
        }
      ).to_h

      runtime = JSON.parse(attrs.fetch(:runtime_json))
      assert_equal ["app.example.com"], runtime.dig("ingress", "hosts")
      assert_equal "api", runtime.dig("ingress", "rules", 0, "target", "service")
      assert_equal "metrics", runtime.dig("ingress", "rules", 0, "target", "port")
      assert_equal "/", runtime.dig("ingress", "rules", 1, "match", "path_prefix")
    end

    test "rejects ingress without hosts" do
      error = assert_raises(RuntimeAttributes::InvalidPayload) do
        RuntimeAttributes.new(
          params: {
            git_sha: "a" * 40,
            image_repository: "api",
            image_digest: "sha256:#{"b" * 64}",
            services: {
              web: {
                ports: [{ name: "http", port: 3000 }],
                healthcheck: { path: "/up", port: 3000 }
              }
            },
            ingress: {
              rules: [
                {
                  match: { host: "app.example.com", path_prefix: "/" },
                  target: { service: "web", port: "http" }
                }
              ]
            }
          }
        ).to_h
      end

      assert_equal "ingress.hosts must include at least one host", error.message
    end

    test "rejects ingress without rules" do
      error = assert_raises(RuntimeAttributes::InvalidPayload) do
        RuntimeAttributes.new(
          params: {
            git_sha: "a" * 40,
            image_repository: "api",
            image_digest: "sha256:#{"b" * 64}",
            services: {
              web: {
                ports: [{ name: "http", port: 3000 }],
                healthcheck: { path: "/up", port: 3000 }
              }
            },
            ingress: {
              hosts: ["app.example.com"]
            }
          }
        ).to_h
      end

      assert_equal "ingress.rules must include at least one rule", error.message
    end
  end
end
