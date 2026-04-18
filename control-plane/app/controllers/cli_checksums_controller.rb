# frozen_string_literal: true

class CliChecksumsController < ActionController::Base
  include PublicArtifactRateLimit
  include ReleaseVersionSelection

  def show
    version = requested_version
    if params[:version].blank?
      redirect_to canonical_version_url(version), allow_other_host: false
    else
      artifact = CliReleases::Fetcher.build.fetch_checksums(version: version)

      response.headers["Cache-Control"] = "public, max-age=31536000, immutable"
      redirect_to artifact.url, allow_other_host: true
    end
  rescue CliReleases::Fetcher::NotConfiguredError => error
    render plain: "cli checksums unavailable: #{error.message}", status: :service_unavailable
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
      stable_version: Devopsellence::RuntimeConfig.current.cli_stable_version,
      edge_version: Devopsellence::RuntimeConfig.current.cli_edge_version,
      stable_env_name: "DEVOPSELLENCE_CLI_STABLE_VERSION",
      edge_env_name: "DEVOPSELLENCE_CLI_EDGE_VERSION",
      not_configured_error_class: CliReleases::Fetcher::NotConfiguredError
    )
  end
end
