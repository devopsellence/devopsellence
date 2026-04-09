# frozen_string_literal: true

require "test_helper"

module ManagedNodes
  module Providers
    class DigitalOceanTest < ActiveSupport::TestCase
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

      test "resolver supports digitalocean" do
        provider = Resolver.resolve("digitalocean", client: FakeClient.new)

        assert_instance_of DigitalOcean, provider
      end

      test "creates droplet with existing ssh key fingerprint" do
        client = FakeClient.new(
          gets: {
            "/account/keys" => Response.new(code: 200, body: JSON.generate(
              ssh_keys: [ { name: "devopsellence", fingerprint: "aa:bb:cc" } ]
            ))
          },
          posts: {
            "/droplets" => Response.new(code: 202, body: JSON.generate(
              droplet: {
                id: 123,
                status: "new",
                networks: {
                  v4: [
                    { type: "public", ip_address: "203.0.113.10" }
                  ]
                }
              }
            ))
          }
        )

        provider = DigitalOcean.new(
          client: client,
          token: "do-token",
          image: "ubuntu-24-04-x64",
          ssh_key_name: "devopsellence",
          ssh_public_key: "ssh-ed25519 AAAA"
        )

        server = provider.create_server(
          name: "pool-node-1",
          region: "nyc3",
          size_slug: "s-1vcpu-1gb",
          user_data: "#!/bin/bash\necho hi"
        )

        request = client.requests.find { |method, path, _payload| method == :post && path == "/droplets" }

        assert_equal "123", server.id
        assert_equal "new", server.status
        assert_equal "203.0.113.10", server.public_ip
        assert_equal [ "aa:bb:cc" ], request[2][:ssh_keys]
        assert_equal "nyc3", request[2][:region]
        assert_equal "s-1vcpu-1gb", request[2][:size]
      end

      test "create server skips runtime-config ssh key outside development" do
        with_env(
          "DEVOPSELLENCE_DIGITALOCEAN_SSH_KEY_NAME" => "devopsellence",
          "DEVOPSELLENCE_DIGITALOCEAN_SSH_PUBLIC_KEY" => "ssh-ed25519 AAAA"
        ) do
          client = FakeClient.new(
            posts: {
              "/droplets" => Response.new(code: 202, body: JSON.generate(
                droplet: {
                  id: 234,
                  status: "new",
                  networks: {
                    v4: [
                      { type: "public", ip_address: "203.0.113.12" }
                    ]
                  }
                }
              ))
            }
          )

          provider = DigitalOcean.new(client: client, token: "do-token")
          provider.create_server(name: "pool-node-0", region: "nyc3", size_slug: "s-1vcpu-1gb", user_data: "#!/bin/bash")

          request = client.requests.find { |method, path, _payload| method == :post && path == "/droplets" }

          assert_nil request[2][:ssh_keys]
        end
      end

      test "create server uses runtime-config ssh key in development" do
        with_env(
          "DEVOPSELLENCE_DIGITALOCEAN_SSH_KEY_NAME" => "devopsellence",
          "DEVOPSELLENCE_DIGITALOCEAN_SSH_PUBLIC_KEY" => "ssh-ed25519 AAAA"
        ) do
          Rails.stubs(:env).returns(ActiveSupport::StringInquirer.new("development"))

          client = FakeClient.new(
            gets: {
              "/account/keys" => Response.new(code: 200, body: JSON.generate(
                ssh_keys: [ { name: "devopsellence", fingerprint: "11:22:33" } ]
              ))
            },
            posts: {
              "/droplets" => Response.new(code: 202, body: JSON.generate(
                droplet: {
                  id: 345,
                  status: "new",
                  networks: {
                    v4: [
                      { type: "public", ip_address: "203.0.113.13" }
                    ]
                  }
                }
              ))
            }
          )

          provider = DigitalOcean.new(client: client, token: "do-token")
          provider.create_server(name: "pool-node-00", region: "nyc3", size_slug: "s-1vcpu-1gb", user_data: "#!/bin/bash")

          request = client.requests.find { |method, path, _payload| method == :post && path == "/droplets" }

          assert_equal [ "11:22:33" ], request[2][:ssh_keys]
        end
      end

      test "creates ssh key when named key does not exist" do
        client = FakeClient.new(
          gets: {
            "/account/keys" => Response.new(code: 200, body: JSON.generate(ssh_keys: []))
          },
          posts: {
            "/account/keys" => Response.new(code: 201, body: JSON.generate(
              ssh_key: { fingerprint: "ff:ee:dd" }
            )),
            "/droplets" => Response.new(code: 202, body: JSON.generate(
              droplet: { id: 456, status: "new", networks: { v4: [] } }
            ))
          }
        )

        provider = DigitalOcean.new(
          client: client,
          token: "do-token",
          image: "ubuntu-24-04-x64",
          ssh_key_name: "devopsellence",
          ssh_public_key: "ssh-ed25519 AAAA"
        )

        provider.create_server(name: "pool-node-2", region: "nyc3", size_slug: "s-1vcpu-1gb", user_data: "#!/bin/bash")

        create_key_request = client.requests.find { |method, path, _payload| method == :post && path == "/account/keys" }
        create_droplet_request = client.requests.find { |method, path, _payload| method == :post && path == "/droplets" }

        assert_equal "devopsellence", create_key_request[2][:name]
        assert_equal [ "ff:ee:dd" ], create_droplet_request[2][:ssh_keys]
      end

      test "raises clear error when digitalocean token is missing" do
        provider = DigitalOcean.new(client: FakeClient.new, token: nil)

        error = assert_raises(RuntimeError) do
          provider.create_server(name: "pool-node-3", region: "nyc3", size_slug: "s-1vcpu-1gb", user_data: "#!/bin/bash")
        end

        assert_equal "configure DEVOPSELLENCE_DIGITALOCEAN_API_TOKEN for managed DigitalOcean nodes", error.message
      end
    end
  end
end
