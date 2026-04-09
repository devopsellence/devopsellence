# frozen_string_literal: true

module PublicArtifactRateLimit
  extend ActiveSupport::Concern

  included do
    rate_limit to: 60, within: 1.minute, scope: "public_artifact_downloads", by: -> { request.remote_ip },
      with: -> { render plain: "too many requests", status: :too_many_requests }
  end
end
