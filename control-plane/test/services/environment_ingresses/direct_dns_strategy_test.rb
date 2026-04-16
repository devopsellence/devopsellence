# frozen_string_literal: true

require "securerandom"
require "test_helper"

module EnvironmentIngresses
  class DirectDnsStrategyTest < ActiveSupport::TestCase
    class FakeClient
      attr_reader :operations

      def initialize(fail_replace: false)
        @fail_replace = fail_replace
        @operations = []
      end

      def delete_dns_records(hostname:, type: nil)
        operations << [ :delete, hostname, type ]
      end

      def replace_dns_a_records(hostname:, addresses:, ttl: 60)
        operations << [ :replace_a, hostname, addresses, ttl ]
        raise "boom" if @fail_replace
      end

      def create_dns_cname(hostname:, target:)
        operations << [ :create_cname, hostname, target ]
      end
    end

    test "keeps tunnel cname in place while no eligible direct dns nodes exist" do
      environment, ingress = build_environment_and_ingress
      client = FakeClient.new

      EligibleNodes.any_instance.stubs(:call).returns([])

      result = DirectDnsStrategy.new(environment:, ingress:, client:).call

      assert_equal ingress, result
      assert_equal EnvironmentIngress::STATUS_DEGRADED, ingress.reload.status
      assert_equal "no eligible public web nodes with fresh heartbeat and settled rollout", ingress.last_error
      assert_equal [], client.operations
    end

    test "restores tunnel cname when direct dns cutover fails after cname delete" do
      environment, ingress = build_environment_and_ingress
      client = FakeClient.new(fail_replace: true)
      node, = issue_test_node!(organization: environment.project.organization, name: "node-a", labels: [ Node::LABEL_WEB ])
      node.capabilities = [ Node::CAPABILITY_DIRECT_DNS_INGRESS ]
      node.public_ip = "198.51.100.10"
      node.provisioning_status = Node::PROVISIONING_READY
      node.ingress_tls_status = Node::INGRESS_TLS_READY
      node.environment = environment
      node.save!

      status = environment.deployments.create!(
        release: environment.current_release,
        sequence: 1,
        request_token: SecureRandom.hex(8),
        status: Deployment::STATUS_PUBLISHED,
        status_message: "rollout settled",
        published_at: Time.current,
        finished_at: Time.current
      ).deployment_node_statuses.create!(
        node: node,
        phase: DeploymentNodeStatus::PHASE_SETTLED,
        message: "ok",
        reported_at: node.updated_at + 1.second
      )
      assert status.persisted?

      error = assert_raises(RuntimeError) do
        DirectDnsStrategy.new(environment:, ingress:, client:).call
      end

      assert_equal "boom", error.message
      assert_equal EnvironmentIngress::STATUS_FAILED, ingress.reload.status
      assert_equal "boom", ingress.last_error
      assert_equal [
        [ :delete, ingress.hostname, "CNAME" ],
        [ :replace_a, ingress.hostname, [ "198.51.100.10" ], 60 ],
        [ :delete, ingress.hostname, "A" ],
        [ :create_cname, ingress.hostname, "#{ingress.cloudflare_tunnel_id}.cfargotunnel.com" ]
      ], client.operations
    end

    test "uses the latest deployment status for eligibility" do
      environment, ingress = build_environment_and_ingress
      client = FakeClient.new
      node, = issue_test_node!(organization: environment.project.organization, name: "node-a", labels: [ Node::LABEL_WEB ])
      node.capabilities = [ Node::CAPABILITY_DIRECT_DNS_INGRESS ]
      node.public_ip = "198.51.100.10"
      node.provisioning_status = Node::PROVISIONING_READY
      node.ingress_tls_status = Node::INGRESS_TLS_READY
      node.environment = environment
      node.save!

      environment.deployments.create!(
        release: environment.current_release,
        sequence: 1,
        request_token: SecureRandom.hex(8),
        status: Deployment::STATUS_PUBLISHED,
        status_message: "rollout settled",
        published_at: Time.current,
        finished_at: Time.current
      ).deployment_node_statuses.create!(
        node: node,
        phase: DeploymentNodeStatus::PHASE_SETTLED,
        message: "ok",
        reported_at: 2.minutes.ago
      )

      environment.deployments.create!(
        release: environment.current_release,
        sequence: 2,
        request_token: SecureRandom.hex(8),
        status: Deployment::STATUS_PUBLISHED,
        status_message: "rollout reconciling",
        published_at: Time.current,
        finished_at: Time.current
      ).deployment_node_statuses.create!(
        node: node,
        phase: DeploymentNodeStatus::PHASE_RECONCILING,
        message: "applying",
        reported_at: 1.minute.ago
      )

      DirectDnsStrategy.new(environment:, ingress:, client:).call

      assert_equal EnvironmentIngress::STATUS_DEGRADED, ingress.reload.status
      assert_equal "no eligible public web nodes with fresh heartbeat and settled rollout", ingress.last_error
      assert_equal [], client.operations
    end

    private

      def build_environment_and_ingress
        organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
        ensure_test_organization_runtime!(organization)
        project = organization.projects.create!(name: "Project A")
        environment = project.environments.create!(
          name: "production",
          gcp_project_id: organization.gcp_project_id,
          gcp_project_number: organization.gcp_project_number,
          workload_identity_pool: organization.workload_identity_pool,
          workload_identity_provider: organization.workload_identity_provider,
          ingress_strategy: Environment::INGRESS_STRATEGY_DIRECT_DNS
        )
        release = project.releases.create!(
          git_sha: "a" * 40,
          revision: "rel-1",
          image_repository: "shop-app",
          image_digest: "sha256:#{"b" * 64}",
          web_json: { port: 3000, healthcheck: { path: "/up", port: 3000 } }.to_json
        )
        environment.update!(current_release: release)
        ingress = environment.create_environment_ingress!(
          hostname: "#{SecureRandom.hex(3)}.devopsellence.io",
          cloudflare_tunnel_id: "tunnel-1",
          gcp_secret_name: "env-#{SecureRandom.hex(4)}-ingress-cloudflare-tunnel-token",
          status: EnvironmentIngress::STATUS_PENDING
        )

        [ environment, ingress ]
      end
  end
end
