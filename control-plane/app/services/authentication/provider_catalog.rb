# frozen_string_literal: true

module Authentication
  module ProviderCatalog
    module_function

    Provider = Struct.new(:key, :label, :path, :button_class, keyword_init: true)

    def enabled
      providers = []
      providers << Provider.new(
        key: "google_oauth2",
        label: "Continue with Google",
        path: "/login/google_oauth2",
        button_class: "w-full rounded bg-white px-4 py-2 text-slate-900 ring-1 ring-slate-300 hover:bg-slate-50"
      ) if google_enabled?
      providers << Provider.new(
        key: "github",
        label: "Continue with GitHub",
        path: "/login/github",
        button_class: "w-full rounded bg-slate-900 px-4 py-2 text-white hover:bg-slate-800"
      ) if github_enabled?
      providers
    end

    def google_enabled?
      runtime.google_client_id.present? && runtime.google_client_secret.present?
    end

    def github_enabled?
      runtime.github_client_id.present? && runtime.github_client_secret.present?
    end

    def runtime
      Devopsellence::RuntimeConfig.current
    end
  end
end
