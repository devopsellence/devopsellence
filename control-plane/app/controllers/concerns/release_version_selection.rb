# frozen_string_literal: true

module ReleaseVersionSelection
  class UnsupportedChannelError < StandardError; end

  private

  def requested_release_version(stable_version:, edge_version:, stable_env_name:, edge_env_name:, not_configured_error_class:)
    params[:version].to_s.presence || configured_release_version(
      stable_version: stable_version,
      edge_version: edge_version,
      stable_env_name: stable_env_name,
      edge_env_name: edge_env_name,
      not_configured_error_class: not_configured_error_class
    )
  end

  def requested_release_channel
    params[:channel].to_s.strip.downcase.presence || "stable"
  end

  def configured_release_version(stable_version:, edge_version:, stable_env_name:, edge_env_name:, not_configured_error_class:)
    case requested_release_channel
    when "stable"
      stable_version.presence || raise(not_configured_error_class, "set #{stable_env_name} or pass ?version=")
    when "edge"
      edge_version.presence || raise(not_configured_error_class, "set #{edge_env_name} or pass ?version=")
    else
      raise UnsupportedChannelError, "unsupported channel #{requested_release_channel.inspect}"
    end
  end
end
