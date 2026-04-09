# frozen_string_literal: true

module PublicBaseUrl
  module_function

  def configured
    Devopsellence::RuntimeConfig.current.public_base_url.presence
  end

  def resolve(request)
    configured || request.base_url
  end
end
