# frozen_string_literal: true

require "test_helper"

module ManagedNodes
  module Providers
    class HetznerTest < ActiveSupport::TestCase
      Response = Struct.new(:code, :body, keyword_init: true)

      class FakeClient
        attr_reader :requests

        def initialize(gets: {}, posts: {}, deletes: {})
          @gets = gets
          @posts = posts
          @deletes = deletes
          @requests = []
        end

        def get(path)
          requests << [ :get, path, nil ]
          @gets.fetch(path)
        end

        def post(path, payload:)
          requests << [ :post, path, payload ]
          @posts.fetch(path)
        end

        def delete(path)
          requests << [ :delete, path, nil ]
          @deletes.fetch(path)
        end
      end

      test "resolver supports hetzner" do
        provider = Resolver.resolve("hetzner", client: FakeClient.new)

        assert_instance_of Hetzner, provider
      end

      test "lists servers across pages" do
        client = FakeClient.new(
          gets: {
            "/servers?page=1" => Response.new(code: 200, body: JSON.generate(
              servers: [
                {
                  id: 101,
                  name: "devopsellence-nodebundle-a1b2c3",
                  status: "running",
                  public_net: { ipv4: { ip: "203.0.113.10" } }
                }
              ],
              meta: { pagination: { next_page: 2 } }
            )),
            "/servers?page=2" => Response.new(code: 200, body: JSON.generate(
              servers: [
                {
                  id: 102,
                  name: "devopsellence-nodebundle-d4e5f6",
                  status: "off",
                  public_net: { ipv4: { ip: "203.0.113.11" } }
                }
              ],
              meta: { pagination: { next_page: nil } }
            ))
          }
        )

        provider = Hetzner.new(client: client, token: "hetzner-token")
        servers = provider.list_servers

        assert_equal [ "101", "102" ], servers.map(&:id)
        assert_equal [ "devopsellence-nodebundle-a1b2c3", "devopsellence-nodebundle-d4e5f6" ], servers.map(&:name)
        assert_equal [ "203.0.113.10", "203.0.113.11" ], servers.map(&:public_ip)
      end

      test "create server skips runtime-config ssh key outside development" do
        with_env(
          "DEVOPSELLENCE_HETZNER_SSH_KEY_NAME" => "devopsellence",
          "DEVOPSELLENCE_HETZNER_SSH_PUBLIC_KEY" => "ssh-ed25519 AAAA"
        ) do
          client = FakeClient.new(
            posts: {
              "/servers" => Response.new(code: 201, body: JSON.generate(
                server: {
                  id: 101,
                  name: "pool-node-1",
                  status: "running",
                  public_net: { ipv4: { ip: "203.0.113.10" } }
                }
              ))
            }
          )

          provider = Hetzner.new(client: client, token: "hetzner-token")
          provider.create_server(name: "pool-node-1", region: "ash", size_slug: "cpx11", user_data: "#!/bin/bash")

          request = client.requests.find { |method, path, _payload| method == :post && path == "/servers" }

          assert_nil request[2][:ssh_keys]
        end
      end

      test "create server uses runtime-config ssh key in development" do
        with_env(
          "DEVOPSELLENCE_HETZNER_SSH_KEY_NAME" => "devopsellence",
          "DEVOPSELLENCE_HETZNER_SSH_PUBLIC_KEY" => "ssh-ed25519 AAAA"
        ) do
          Rails.stubs(:env).returns(ActiveSupport::StringInquirer.new("development"))

          client = FakeClient.new(
            gets: {
              "/ssh_keys" => Response.new(code: 200, body: JSON.generate(
                ssh_keys: [ { name: "devopsellence" } ]
              ))
            },
            posts: {
              "/servers" => Response.new(code: 201, body: JSON.generate(
                server: {
                  id: 102,
                  name: "pool-node-2",
                  status: "running",
                  public_net: { ipv4: { ip: "203.0.113.11" } }
                }
              ))
            }
          )

          provider = Hetzner.new(client: client, token: "hetzner-token")
          provider.create_server(name: "pool-node-2", region: "ash", size_slug: "cpx11", user_data: "#!/bin/bash")

          request = client.requests.find { |method, path, _payload| method == :post && path == "/servers" }

          assert_equal [ "devopsellence" ], request[2][:ssh_keys]
        end
      end

      test "raises clear error when hetzner token is missing" do
        provider = Hetzner.new(client: FakeClient.new, token: nil)

        error = assert_raises(RuntimeError) do
          provider.list_servers
        end

        assert_equal "configure DEVOPSELLENCE_HETZNER_API_TOKEN for managed Hetzner nodes", error.message
      end
    end
  end
end
