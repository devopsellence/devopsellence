# frozen_string_literal: true

require "test_helper"

class AgentReleasesContainerImageTest < ActiveSupport::TestCase
  test "prefers explicit container image reference" do
    with_env(
      "DEVOPSELLENCE_AGENT_CONTAINER_IMAGE" => "ghcr.io/devopsellence/agent:v1.2.3",
      "DEVOPSELLENCE_AGENT_CONTAINER_REPOSITORY" => "us-east1-docker.pkg.dev/devopsellence/agents/devopsellence-agent",
      "DEVOPSELLENCE_STABLE_VERSION" => "v9.9.9"
    ) do
      assert_equal(
        {
          reference: "ghcr.io/devopsellence/agent:v1.2.3",
          version: "v9.9.9"
        },
        AgentReleases::ContainerImage.metadata
      )
    end
  end

  test "builds image reference from repository and stable version" do
    with_env(
      "DEVOPSELLENCE_AGENT_CONTAINER_IMAGE" => nil,
      "DEVOPSELLENCE_AGENT_CONTAINER_REPOSITORY" => "us-east1-docker.pkg.dev/devopsellence/agents/devopsellence-agent",
      "DEVOPSELLENCE_STABLE_VERSION" => "v1.2.3"
    ) do
      assert_equal(
        {
          reference: "us-east1-docker.pkg.dev/devopsellence/agents/devopsellence-agent:v1.2.3",
          version: "v1.2.3"
        },
        AgentReleases::ContainerImage.metadata
      )
    end
  end

  test "returns nil when no container metadata is configured" do
    with_env(
      "DEVOPSELLENCE_AGENT_CONTAINER_IMAGE" => nil,
      "DEVOPSELLENCE_AGENT_CONTAINER_REPOSITORY" => nil,
      "DEVOPSELLENCE_STABLE_VERSION" => nil
    ) do
      assert_nil AgentReleases::ContainerImage.metadata
    end
  end
end
