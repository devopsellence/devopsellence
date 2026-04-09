# frozen_string_literal: true

Rails.application.config.middleware.use OmniAuth::Builder do
  if Authentication::ProviderCatalog.google_enabled?
    provider :google_oauth2,
      Devopsellence::RuntimeConfig.current.google_client_id,
      Devopsellence::RuntimeConfig.current.google_client_secret,
      scope: "openid,email",
      prompt: "select_account"
  end

  if Authentication::ProviderCatalog.github_enabled?
    provider :github,
      Devopsellence::RuntimeConfig.current.github_client_id,
      Devopsellence::RuntimeConfig.current.github_client_secret,
      scope: "read:user,user:email"
  end
end

OmniAuth.config.path_prefix = "/login"
OmniAuth.config.allowed_request_methods = [ :post ]
OmniAuth.config.silence_get_warning = true
OmniAuth.config.full_host = lambda do |env|
  PublicBaseUrl.resolve(ActionDispatch::Request.new(env))
end
