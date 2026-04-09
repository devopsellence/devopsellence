# frozen_string_literal: true

require "test_helper"

module Runtime
  class BundlesReconcilerTest < ActiveSupport::TestCase
    test "reconciler seeds the default runtime project before iterating" do
      runtime_project = Object.new
      ordering = sequence("default-before-lock")

      reconciler = BundlesReconciler.new
      RuntimeProject.expects(:default!).returns(runtime_project).once.in_sequence(ordering)
      reconciler.expects(:with_reconciler_lock).once.in_sequence(ordering).yields
      WarmServers::PoolReconciler.any_instance.expects(:call).once.in_sequence(ordering)
      RuntimeProject.expects(:find_each).once.in_sequence(ordering).yields(runtime_project)
      reconciler.expects(:ensure_organization_bundles!).with(runtime_project: runtime_project).once.in_sequence(ordering)

      reconciler.call
    end

    test "reconciler serializes the bundle tree refill under the global advisory lock" do
      runtime_project = Object.new
      ordering = sequence("reconciler-lock")

      reconciler = BundlesReconciler.new
      RuntimeProject.expects(:default!).once.in_sequence(ordering)
      reconciler.expects(:with_reconciler_lock).once.in_sequence(ordering).yields
      WarmServers::PoolReconciler.any_instance.expects(:call).once.in_sequence(ordering)
      RuntimeProject.expects(:find_each).once.in_sequence(ordering).yields(runtime_project)
      reconciler.expects(:ensure_organization_bundles!).with(runtime_project: runtime_project).once.in_sequence(ordering)

      reconciler.call
    end

    test "reconciler keeps warm server refill inside the global reconciliation lock" do
      runtime_project = Object.new
      ordering = sequence("warm-servers-inside-lock")

      reconciler = BundlesReconciler.new
      RuntimeProject.expects(:default!).once.in_sequence(ordering)
      reconciler.expects(:with_reconciler_lock).once.in_sequence(ordering).yields
      WarmServers::PoolReconciler.any_instance.expects(:call).once.in_sequence(ordering)
      RuntimeProject.expects(:find_each).once.in_sequence(ordering).yields(runtime_project)
      reconciler.expects(:ensure_organization_bundles!).with(runtime_project: runtime_project).once.in_sequence(ordering)

      reconciler.call
    end

    test "reconciler provisions warm servers before bundle tree refill" do
      runtime_project = Object.new
      ordering = sequence("warm-before-bundles")

      reconciler = BundlesReconciler.new
      RuntimeProject.expects(:default!).once.in_sequence(ordering)
      reconciler.expects(:with_reconciler_lock).once.in_sequence(ordering).yields
      WarmServers::PoolReconciler.any_instance.expects(:call).once.in_sequence(ordering)
      RuntimeProject.expects(:find_each).once.in_sequence(ordering).yields(runtime_project)
      reconciler.expects(:ensure_organization_bundles!).with(runtime_project: runtime_project).once.in_sequence(ordering)

      reconciler.call
    end

    test "claimed bundles do not satisfy spare targets and still get spare descendants" do
      runtime_project = RuntimeProject.default!
      organization = Organization.create!(name: "Acme", provisioning_status: Organization::PROVISIONING_READY)
      project = Project.create!(organization:, name: "App")
      environment = Environment.create!(project:, name: "production")
      node, = issue_test_node!(
        organization:,
        managed: true,
        managed_provider: Devopsellence::RuntimeConfig.current.managed_default_provider,
        managed_region: Devopsellence::RuntimeConfig.current.managed_default_region,
        managed_size_slug: Devopsellence::RuntimeConfig.current.managed_default_size_slug,
        provider_server_id: "srv-claimed"
      )

      organization_bundle = OrganizationBundle.create!(
        runtime_project:,
        status: OrganizationBundle::STATUS_CLAIMED,
        claimed_by_organization: organization
      )
      organization.update!(organization_bundle:, runtime_project:)

      environment_bundle = EnvironmentBundle.create!(
        runtime_project:,
        organization_bundle:,
        status: EnvironmentBundle::STATUS_CLAIMED,
        claimed_by_environment: environment
      )
      environment.update!(environment_bundle:, runtime_project:)

      node_bundle = NodeBundle.create!(
        runtime_project:,
        organization_bundle:,
        environment_bundle:,
        node:,
        status: NodeBundle::STATUS_CLAIMED
      )
      node.update!(environment:, node_bundle:)

      with_runtime_config(
        organization_bundle_target: 1,
        environment_bundle_target: 1,
        node_bundle_target: 1
      ) do
        organization_provisioner_class = Class.new do
          def initialize(runtime_project:, broker: nil)
            @runtime_project = runtime_project
          end

          def call
            OrganizationBundle.create!(
              runtime_project: @runtime_project,
              status: OrganizationBundle::STATUS_WARM
            )
          end
        end

        environment_provisioner_class = Class.new do
          def initialize(organization_bundle:, broker: nil)
            @organization_bundle = organization_bundle
          end

          def call
            EnvironmentBundle.create!(
              runtime_project: @organization_bundle.runtime_project,
              organization_bundle: @organization_bundle,
              status: EnvironmentBundle::STATUS_WARM
            )
          end
        end

        node_provisioner_class = Class.new do
          def initialize(environment_bundle:, broker: nil)
            @environment_bundle = environment_bundle
          end

          def call
            NodeBundle.create!(
              runtime_project: @environment_bundle.runtime_project,
              organization_bundle: @environment_bundle.organization_bundle,
              environment_bundle: @environment_bundle,
              status: NodeBundle::STATUS_WARM
            )
          end
        end

        RuntimeProject.stubs(:find_each).yields(runtime_project)
        WarmServers::PoolReconciler.any_instance.stubs(:call).returns(nil)

        BundlesReconciler.new(
          organization_provisioner_class: organization_provisioner_class,
          environment_provisioner_class: environment_provisioner_class,
          node_provisioner_class: node_provisioner_class
        ).call
      end

      assert_operator OrganizationBundle.count, :>=, 2
      assert_operator OrganizationBundle.warm.count, :>=, 1
      assert_equal OrganizationBundle::STATUS_CLAIMED, organization_bundle.reload.status

      assert_operator EnvironmentBundle.count, :>=, 3
      assert_operator EnvironmentBundle.warm.count, :>=, 2
      assert_equal EnvironmentBundle::STATUS_CLAIMED, environment_bundle.reload.status

      assert_operator NodeBundle.count, :>=, 4
      assert_operator NodeBundle.warm.count, :>=, 3
      assert_equal NodeBundle::STATUS_CLAIMED, node_bundle.reload.status

      claimed_env_spare = environment_bundle.node_bundles.warm.first
      assert claimed_env_spare.present?
      spare_env_under_claimed_org = organization_bundle.environment_bundles.find_by!(status: EnvironmentBundle::STATUS_WARM)
      assert_equal 1, spare_env_under_claimed_org.node_bundles.warm.count
      spare_org = OrganizationBundle.find_by!(status: OrganizationBundle::STATUS_WARM)
      assert_equal 1, spare_org.environment_bundles.warm.first.node_bundles.warm.count
    end

    test "claimed environment bundles refill node bundles before spare environment bundles" do
      runtime_project = RuntimeProject.default!
      organization = Organization.create!(name: "Acme", provisioning_status: Organization::PROVISIONING_READY)
      project = Project.create!(organization:, name: "App")
      environment = Environment.create!(project:, name: "production")

      organization_bundle = OrganizationBundle.create!(
        runtime_project:,
        status: OrganizationBundle::STATUS_CLAIMED,
        claimed_by_organization: organization
      )
      organization.update!(organization_bundle:, runtime_project:)

      environment_bundle = EnvironmentBundle.create!(
        runtime_project:,
        organization_bundle:,
        status: EnvironmentBundle::STATUS_CLAIMED,
        claimed_by_environment: environment
      )
      environment.update!(environment_bundle:, runtime_project:)

      calls = []

      organization_provisioner_class = Class.new do
        def initialize(runtime_project:, broker: nil)
          @runtime_project = runtime_project
        end

        def call
          OrganizationBundle.create!(
            runtime_project: @runtime_project,
            status: OrganizationBundle::STATUS_WARM
          )
        end
      end

      environment_provisioner_class = Class.new do
        class << self
          attr_accessor :calls
        end

        def initialize(organization_bundle:, broker: nil)
          @organization_bundle = organization_bundle
        end

        def call
          self.class.instance_variable_get(:@calls) << [ :environment, @organization_bundle.token ]
          EnvironmentBundle.create!(
            runtime_project: @organization_bundle.runtime_project,
            organization_bundle: @organization_bundle,
            status: EnvironmentBundle::STATUS_WARM
          )
        end
      end
      environment_provisioner_class.calls = calls

      node_provisioner_class = Class.new do
        class << self
          attr_accessor :calls
        end

        def initialize(environment_bundle:, broker: nil)
          @environment_bundle = environment_bundle
        end

        def call
          self.class.instance_variable_get(:@calls) << [ :node, @environment_bundle.token ]
          NodeBundle.create!(
            runtime_project: @environment_bundle.runtime_project,
            organization_bundle: @environment_bundle.organization_bundle,
            environment_bundle: @environment_bundle,
            status: NodeBundle::STATUS_WARM
          )
        end
      end
      node_provisioner_class.calls = calls

      with_runtime_config(
        organization_bundle_target: 1,
        environment_bundle_target: 1,
        node_bundle_target: 1
      ) do
        RuntimeProject.stubs(:find_each).yields(runtime_project)
        WarmServers::PoolReconciler.any_instance.stubs(:call).returns(nil)

        BundlesReconciler.new(
          organization_provisioner_class: organization_provisioner_class,
          environment_provisioner_class: environment_provisioner_class,
          node_provisioner_class: node_provisioner_class
        ).call
      end

      assert_equal [ :node, environment_bundle.token ], calls.first
      assert_includes calls, [ :environment, organization_bundle.token ]
      assert_equal 1, environment_bundle.node_bundles.warm.count
    end

    test "reconciler fails stale provisioning bundles before refilling capacity" do
      runtime_project = RuntimeProject.default!
      organization_bundle = OrganizationBundle.create!(
        runtime_project:,
        status: OrganizationBundle::STATUS_WARM
      )
      environment_bundle = EnvironmentBundle.create!(
        runtime_project:,
        organization_bundle:,
        status: EnvironmentBundle::STATUS_WARM
      )
      stale_bundle = NodeBundle.create!(
        runtime_project:,
        organization_bundle:,
        environment_bundle:,
        status: NodeBundle::STATUS_PROVISIONING
      )
      stale_bundle.update_columns(updated_at: 20.minutes.ago)

      with_runtime_config(
        organization_bundle_target: 1,
        environment_bundle_target: 1,
        node_bundle_target: 1,
        bundle_provisioning_timeout_seconds: 60
      ) do
        node_provisioner_class = Class.new do
          def initialize(environment_bundle:, broker: nil)
            @environment_bundle = environment_bundle
          end

          def call
            NodeBundle.create!(
              runtime_project: @environment_bundle.runtime_project,
              organization_bundle: @environment_bundle.organization_bundle,
              environment_bundle: @environment_bundle,
              status: NodeBundle::STATUS_WARM
            )
          end
        end

        RuntimeProject.stubs(:find_each).yields(runtime_project)
        WarmServers::PoolReconciler.any_instance.stubs(:call).returns(nil)

        BundlesReconciler.new(node_provisioner_class: node_provisioner_class).call
      end

      assert_equal NodeBundle::STATUS_FAILED, stale_bundle.reload.status
      assert_equal "provisioning timed out", stale_bundle.provisioning_error
      assert_equal 1, environment_bundle.node_bundles.warm.count
    end

    test "reconciler does not provision environment bundles under provisioning organization bundles" do
      runtime_project = RuntimeProject.default!
      organization_bundle = OrganizationBundle.create!(
        runtime_project:,
        status: OrganizationBundle::STATUS_PROVISIONING
      )
      calls = []

      environment_provisioner_class = Class.new do
        class << self
          attr_accessor :calls
        end

        def initialize(organization_bundle:, broker: nil)
          @organization_bundle = organization_bundle
        end

        def call
          self.class.instance_variable_get(:@calls) << @organization_bundle.token
          EnvironmentBundle.create!(
            runtime_project: @organization_bundle.runtime_project,
            organization_bundle: @organization_bundle,
            status: EnvironmentBundle::STATUS_WARM
          )
        end
      end
      environment_provisioner_class.calls = calls

      with_runtime_config(
        organization_bundle_target: 1,
        environment_bundle_target: 1,
        node_bundle_target: 0
      ) do
        RuntimeProject.stubs(:find_each).yields(runtime_project)
        WarmServers::PoolReconciler.any_instance.stubs(:call).returns(nil)

        BundlesReconciler.new(environment_provisioner_class: environment_provisioner_class).call
      end

      assert_empty calls
      assert_equal 0, organization_bundle.environment_bundles.count
    end

    test "reconciler does not provision node bundles under provisioning environment bundles" do
      runtime_project = RuntimeProject.default!
      organization_bundle = OrganizationBundle.create!(
        runtime_project:,
        status: OrganizationBundle::STATUS_WARM
      )
      environment_bundle = EnvironmentBundle.create!(
        runtime_project:,
        organization_bundle:,
        status: EnvironmentBundle::STATUS_PROVISIONING
      )
      calls = []

      node_provisioner_class = Class.new do
        class << self
          attr_accessor :calls
        end

        def initialize(environment_bundle:, broker: nil)
          @environment_bundle = environment_bundle
        end

        def call
          self.class.instance_variable_get(:@calls) << @environment_bundle.token
          NodeBundle.create!(
            runtime_project: @environment_bundle.runtime_project,
            organization_bundle: @environment_bundle.organization_bundle,
            environment_bundle: @environment_bundle,
            status: NodeBundle::STATUS_WARM
          )
        end
      end
      node_provisioner_class.calls = calls

      with_runtime_config(
        organization_bundle_target: 1,
        environment_bundle_target: 1,
        node_bundle_target: 1
      ) do
        RuntimeProject.stubs(:find_each).yields(runtime_project)
        WarmServers::PoolReconciler.any_instance.stubs(:call).returns(nil)

        BundlesReconciler.new(node_provisioner_class: node_provisioner_class).call
      end

      assert_empty calls
      assert_equal 0, environment_bundle.node_bundles.count
    end
  end
end
