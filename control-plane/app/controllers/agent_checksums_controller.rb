# frozen_string_literal: true

class AgentChecksumsController < ActionController::Base
  include PublicArtifactRateLimit

  def show
    version = requested_version
    if params[:version].blank?
      redirect_to canonical_version_url(version), allow_other_host: false
    else
      artifact = AgentReleases::Fetcher.build.fetch_checksums(version: version)

      response.headers["Cache-Control"] = "public, max-age=31536000, immutable"
      send_data artifact.body,
        filename: artifact.filename,
        type: "text/plain",
        disposition: "attachment"
    end
  rescue AgentReleases::Fetcher::NotConfiguredError => error
    render plain: "agent checksums unavailable: #{error.message}", status: :service_unavailable
  rescue AgentReleases::Fetcher::NotFoundError => error
    render plain: error.message, status: :not_found
  rescue AgentReleases::Fetcher::Error => error
    render plain: "agent checksum download failed: #{error.message}", status: :bad_gateway
  end

  private

  def canonical_version_url(version)
    query = request.query_parameters.merge("version" => version).to_query
    "#{request.path}?#{query}"
  end

  def requested_version
    params[:version].to_s.presence || Devopsellence::RuntimeConfig.current.agent_stable_version.presence || raise(
      AgentReleases::Fetcher::NotConfiguredError,
      "set DEVOPSELLENCE_AGENT_STABLE_VERSION or pass ?version="
    )
  end
end
