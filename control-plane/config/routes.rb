Rails.application.routes.draw do
  # Define your application routes per the DSL in https://guides.rubyonrails.org/routing.html

  # Reveal health status on /up that returns 200 if the app boots with no exceptions, otherwise 500.
  # Can be used by load balancers and uptime monitors to verify that the app is live.
  get "up" => "rails/health#show", as: :rails_health_check

  # Render dynamic PWA files from app/views/pwa/* (remember to link manifest in application.html.erb)
  # get "manifest" => "rails/pwa#manifest", as: :pwa_manifest
  # get "service-worker" => "rails/pwa#service_worker", as: :pwa_service_worker

  # Defines the root path route ("/")
  root "marketing#index"
  get "getting-started" => "marketing#index", as: :getting_started
  get "docs" => "marketing#docs", as: :docs
  get "roadmap" => "marketing#roadmap", as: :roadmap
  get "blog" => "marketing#blog_index", as: :blog
  get "blog/:slug" => "marketing#blog_show", as: :blog_post
  get "privacy" => "marketing#privacy", as: :privacy
  get "terms" => "marketing#terms", as: :terms

  get "login" => "logins#new"
  post "login" => "logins#create"
  get "login/verify" => "logins#verify"
  get "claim/verify" => "account_claims#verify", as: :claim_verify
  match "login/google_oauth2/callback" => "oauth_logins#callback", via: [ :get, :post ]
  match "login/github/callback" => "oauth_logins#callback", via: [ :get, :post ]
  match "login/failure" => "oauth_logins#failure", via: [ :get, :post ]
  delete "logout" => "sessions#destroy"

  get "cli/login" => "logins#new"
  get "setup/github-actions" => "setup/github_actions#show", as: :setup_github_actions
  post "setup/github-actions" => "setup/github_actions#create"
  get "dashboard" => "dashboard#index"
  post "dashboard/organizations" => "dashboard#create_organization", as: :dashboard_organizations
  post "dashboard/organizations/:organization_id/bootstrap" => "dashboard#bootstrap_node", as: :dashboard_bootstrap_node
  post "dashboard/organizations/:organization_id/projects" => "dashboard#create_project", as: :dashboard_projects
  post "dashboard/projects/:project_id/environments" => "dashboard#create_environment", as: :dashboard_environments
  post "dashboard/environments/:environment_id/assignments" => "dashboard#assign_node", as: :dashboard_assignments
  post "dashboard/environments/:environment_id/secrets" => "dashboard#upsert_environment_secret", as: :dashboard_environment_secrets
  post "dashboard/nodes/:node_id/labels" => "dashboard#update_node_labels", as: :dashboard_node_labels
  post "dashboard/projects/:project_id/releases" => "dashboard#create_release", as: :dashboard_releases
  post "dashboard/releases/:release_id/publish" => "dashboard#publish_release", as: :dashboard_publish_release
  get "install.sh" => "installs#show"
  get "uninstall.sh" => "installs#uninstall"
  get "agent/download" => "agent_downloads#show"
  get "agent/checksums" => "agent_checksums#show"
  get "lfg.sh" => "cli_installs#show"
  get "cli/download" => "cli_downloads#show"
  get "cli/checksums" => "cli_checksums#show"
  get ".well-known/openid-configuration" => "idp#openid_configuration"
  get ".well-known/jwks.json" => "idp#jwks"
  get ".well-known/devopsellence-desired-state-jwks.json" => "idp#desired_state_jwks"

  namespace :api do
    namespace :v1 do
      namespace :public do
        namespace :cli do
          post "bootstrap" => "bootstraps#create"
        end
      end

      namespace :cli do
        post "auth/start" => "auth#start"
        post "auth/token" => "auth#token"
        post "auth/refresh" => "auth#refresh"
        post "account/claim/start" => "account_claims#create"
        get "organizations" => "organizations#index"
        post "organizations" => "organizations#create"
        post "deploy_target" => "deploy_targets#create"
        get "organizations/:organization_id/nodes" => "nodes#index"
        post "organizations/:organization_id/node_bootstrap_tokens" => "nodes#create_bootstrap_token"
        post "nodes/:node_id/diagnose_requests" => "node_diagnose_requests#create"
        get "node_diagnose_requests/:id" => "node_diagnose_requests#show"
        get "projects" => "projects#index"
        post "projects" => "projects#create"
        delete "projects/:id" => "projects#destroy"
        get "organizations/:organization_id/registry" => "organization_registries#show"
        post "organizations/:organization_id/registry" => "organization_registries#upsert"
        patch "organizations/:organization_id/registry" => "organization_registries#upsert"
        get "projects/:project_id/environments" => "environments#index"
        post "projects/:project_id/environments" => "environments#create"
        patch "environments/:environment_id/ingress" => "environments#update_ingress"
        delete "environments/:environment_id" => "environments#destroy"
        get "tokens" => "tokens#index"
        post "tokens" => "tokens#create"
        delete "tokens/:id" => "tokens#destroy"
        post "projects/:project_id/registry/push_auth" => "registry#push_auth"
        post "projects/:project_id/gar/push_auth" => "gar#push_auth"
        post "projects/:project_id/releases" => "releases#create"
        get "environments/:environment_id/status" => "environment_statuses#show"
        get "environments/:environment_id/secrets" => "environment_secrets#index"
        post "environments/:environment_id/secrets" => "environment_secrets#create"
        delete "environments/:environment_id/secrets/:service_name/:name" => "environment_secrets#destroy"
        post "releases/:id/publish" => "releases#publish"
        get "deployments/:id" => "deployments#show"
        post "environments/:environment_id/assignments" => "assignments#create"
        delete "nodes/:node_id/assignment" => "node_assignments#destroy"
        post "nodes/:id/labels" => "nodes#update_labels"
        delete "nodes/:id" => "nodes#destroy"
      end

      namespace :agent do
        post "bootstrap" => "bootstrap#create"
        post "auth/refresh" => "auth#refresh"
        get "assignment" => "assignments#show"
        post "diagnose_requests/claim" => "diagnose_requests#claim"
        post "diagnose_requests/:id/result" => "diagnose_requests#create_result"
        get "desired_state" => "desired_states#show"
        post "ingress_certificates" => "ingress_certificates#create"
        post "registry_auth" => "registry_auth#create"
        get "secrets/environment_bundles/:id/tunnel_token" => "secrets#show_environment_bundle_tunnel_token"
        get "secrets/environment_secrets/:id" => "secrets#show_environment_secret"
        post "sts/token" => "sts#create"
        post "status" => "statuses#create"
      end
    end
  end
end
