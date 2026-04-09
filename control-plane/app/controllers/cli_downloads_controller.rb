# frozen_string_literal: true

class CliDownloadsController < ActionController::Base
  include PublicArtifactRateLimit

  def show
    version = requested_version
    if params[:version].blank?
      redirect_to canonical_version_url(version), allow_other_host: false
    else
      artifact = CliReleases::Fetcher.build.fetch(
        version: version,
        os: params.fetch(:os, "linux").to_s,
        arch: params.fetch(:arch, "amd64").to_s
      )

      response.headers["Cache-Control"] = "public, max-age=31536000, immutable"
      send_data artifact.body,
        filename: artifact.filename,
        type: "application/octet-stream",
        disposition: "attachment"
    end
  rescue CliReleases::Fetcher::NotConfiguredError => error
    render plain: "cli binary unavailable: #{error.message}", status: :service_unavailable
  rescue CliReleases::Fetcher::UnsupportedTargetError => error
    render plain: error.message, status: :unprocessable_entity
  rescue CliReleases::Fetcher::NotFoundError => error
    render plain: error.message, status: :not_found
  rescue CliReleases::Fetcher::Error => error
    render plain: "cli binary download failed: #{error.message}", status: :bad_gateway
  end

  private

  def canonical_version_url(version)
    query = request.query_parameters.merge("version" => version).to_query
    "#{request.path}?#{query}"
  end

  def requested_version
    params[:version].to_s.presence || Devopsellence::RuntimeConfig.current.cli_stable_version.presence || raise(
      CliReleases::Fetcher::NotConfiguredError,
      "set DEVOPSELLENCE_CLI_STABLE_VERSION or pass ?version="
    )
  end
end
