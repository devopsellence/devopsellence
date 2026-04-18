# frozen_string_literal: true

class AgentDownloadsController < ActionController::Base
  include PublicArtifactRateLimit
  include ReleaseVersionSelection

  def show
    version = requested_version
    if params[:version].blank?
      redirect_to canonical_version_url(version), allow_other_host: false
    else
      artifact = AgentReleases::Fetcher.build.fetch(
        version: version,
        os: params.fetch(:os, "linux").to_s,
        arch: params.fetch(:arch, "amd64").to_s
      )

      response.headers["Cache-Control"] = "public, max-age=31536000, immutable"
      redirect_to artifact.url, allow_other_host: true
    end
  rescue AgentReleases::Fetcher::NotConfiguredError => error
    render plain: "agent binary unavailable: #{error.message}", status: :service_unavailable
  rescue AgentReleases::Fetcher::UnsupportedTargetError => error
    render plain: error.message, status: :unprocessable_entity
  rescue ReleaseVersionSelection::UnsupportedChannelError => error
    render plain: error.message, status: :unprocessable_entity
  end

  private

  def canonical_version_url(version)
    query = request.query_parameters.merge("version" => version).to_query
    "#{request.path}?#{query}"
  end

  def requested_version
    requested_release_version(
      stable_version: Devopsellence::RuntimeConfig.current.agent_stable_version,
      edge_version: Devopsellence::RuntimeConfig.current.agent_edge_version,
      stable_env_name: "DEVOPSELLENCE_AGENT_STABLE_VERSION",
      edge_env_name: "DEVOPSELLENCE_AGENT_EDGE_VERSION",
      not_configured_error_class: AgentReleases::Fetcher::NotConfiguredError
    )
  end
end
