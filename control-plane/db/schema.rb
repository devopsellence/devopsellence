# This file is auto-generated from the current state of the database. Instead
# of editing this file, please use the migrations feature of Active Record to
# incrementally modify your database, and then regenerate this schema definition.
#
# This file is the source Rails uses to define your schema when running `bin/rails
# db:schema:load`. When creating a new database, `bin/rails db:schema:load` tends to
# be faster and is potentially less error prone than running all of your
# migrations from scratch. Old migrations may fail to apply correctly if those
# migrations use external dependencies or application code.
#
# It's strongly recommended that you check this file into your version control system.

ActiveRecord::Schema[8.1].define(version: 2026_04_04_010000) do
  # These are extensions that must be enabled in order to support this database
  enable_extension "pg_catalog.plpgsql"

  create_table "api_tokens", force: :cascade do |t|
    t.datetime "access_expires_at", null: false
    t.string "access_token_digest", null: false
    t.datetime "created_at", null: false
    t.datetime "last_used_at"
    t.string "name"
    t.datetime "refresh_expires_at", null: false
    t.string "refresh_token_digest", null: false
    t.datetime "revoked_at"
    t.datetime "updated_at", null: false
    t.integer "user_id", null: false
    t.index ["access_expires_at"], name: "index_api_tokens_on_access_expires_at"
    t.index ["access_token_digest"], name: "index_api_tokens_on_access_token_digest", unique: true
    t.index ["refresh_token_digest"], name: "index_api_tokens_on_refresh_token_digest", unique: true
    t.index ["user_id"], name: "index_api_tokens_on_user_id"
  end

  create_table "claim_links", force: :cascade do |t|
    t.datetime "consumed_at"
    t.datetime "created_at", null: false
    t.string "email", null: false
    t.datetime "expires_at", null: false
    t.string "ip_address"
    t.string "token_digest", null: false
    t.datetime "updated_at", null: false
    t.string "user_agent"
    t.integer "user_id", null: false
    t.index ["expires_at"], name: "index_claim_links_on_expires_at"
    t.index ["token_digest"], name: "index_claim_links_on_token_digest", unique: true
    t.index ["user_id"], name: "index_claim_links_on_user_id"
  end

  create_table "deployment_node_statuses", force: :cascade do |t|
    t.text "containers_json"
    t.datetime "created_at", null: false
    t.integer "deployment_id", null: false
    t.text "error_message"
    t.text "message"
    t.integer "node_id", null: false
    t.string "phase", default: "pending", null: false
    t.datetime "reported_at"
    t.datetime "updated_at", null: false
    t.index ["deployment_id", "node_id"], name: "index_deployment_node_statuses_on_deployment_id_and_node_id", unique: true
    t.index ["deployment_id"], name: "index_deployment_node_statuses_on_deployment_id"
    t.index ["node_id", "updated_at"], name: "index_deployment_node_statuses_on_node_id_and_updated_at"
    t.index ["node_id"], name: "index_deployment_node_statuses_on_node_id"
  end

  create_table "deployments", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.integer "environment_id", null: false
    t.text "error_message"
    t.datetime "finished_at"
    t.datetime "published_at", null: false
    t.bigint "release_command_node_id"
    t.string "release_command_status"
    t.integer "release_id", null: false
    t.string "request_token", null: false
    t.integer "sequence", null: false
    t.string "status", default: "published", null: false
    t.string "status_message"
    t.datetime "updated_at", null: false
    t.index ["environment_id", "release_id"], name: "index_deployments_on_environment_id_and_release_id"
    t.index ["environment_id", "request_token"], name: "index_deployments_on_environment_id_and_request_token", unique: true
    t.index ["environment_id", "sequence"], name: "index_deployments_on_environment_id_and_sequence", unique: true
    t.index ["environment_id"], name: "index_deployments_on_environment_id"
    t.index ["release_command_node_id"], name: "index_deployments_on_release_command_node_id"
    t.index ["release_id"], name: "index_deployments_on_release_id"
  end

  create_table "environment_bundles", force: :cascade do |t|
    t.datetime "claimed_at"
    t.integer "claimed_by_environment_id"
    t.string "cloudflare_tunnel_id"
    t.datetime "created_at", null: false
    t.string "gcp_secret_name", null: false
    t.string "hostname"
    t.integer "organization_bundle_id", null: false
    t.datetime "provisioned_at"
    t.text "provisioning_error"
    t.integer "runtime_project_id", null: false
    t.string "service_account_email", null: false
    t.string "status", default: "provisioning", null: false
    t.string "token", null: false
    t.text "tunnel_token"
    t.datetime "updated_at", null: false
    t.index ["claimed_by_environment_id"], name: "index_environment_bundles_on_claimed_by_environment_id"
    t.index ["gcp_secret_name"], name: "index_environment_bundles_on_gcp_secret_name", unique: true
    t.index ["hostname"], name: "index_environment_bundles_on_hostname", unique: true
    t.index ["organization_bundle_id", "status"], name: "index_env_bundles_on_org_bundle_and_status"
    t.index ["organization_bundle_id"], name: "index_environment_bundles_on_organization_bundle_id"
    t.index ["runtime_project_id"], name: "index_environment_bundles_on_runtime_project_id"
    t.index ["service_account_email"], name: "index_environment_bundles_on_service_account_email", unique: true
    t.index ["token"], name: "index_environment_bundles_on_token", unique: true
  end

  create_table "environment_ingresses", force: :cascade do |t|
    t.string "cloudflare_tunnel_id", default: "", null: false
    t.datetime "created_at", null: false
    t.integer "environment_id", null: false
    t.string "gcp_secret_name", null: false
    t.string "hostname", null: false
    t.text "last_error"
    t.datetime "provisioned_at"
    t.string "status", default: "pending", null: false
    t.datetime "updated_at", null: false
    t.index ["environment_id"], name: "index_environment_ingresses_on_environment_id", unique: true
    t.index ["gcp_secret_name"], name: "index_environment_ingresses_on_gcp_secret_name", unique: true
    t.index ["hostname"], name: "index_environment_ingresses_on_hostname", unique: true
  end

  create_table "environment_secrets", force: :cascade do |t|
    t.string "access_grantee_email"
    t.datetime "access_verified_at"
    t.datetime "created_at", null: false
    t.integer "environment_id", null: false
    t.string "gcp_secret_name", null: false
    t.string "name", null: false
    t.string "service_name", null: false
    t.datetime "updated_at", null: false
    t.text "value"
    t.string "value_sha256"
    t.index ["environment_id", "service_name", "name"], name: "idx_environment_secrets_on_scope", unique: true
    t.index ["environment_id"], name: "index_environment_secrets_on_environment_id"
    t.index ["gcp_secret_name"], name: "index_environment_secrets_on_gcp_secret_name", unique: true
  end

  create_table "environments", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.integer "current_release_id"
    t.integer "environment_bundle_id"
    t.string "gcp_project_id", null: false
    t.string "gcp_project_number", null: false
    t.integer "identity_version", default: 1, null: false
    t.string "ingress_strategy", default: "tunnel", null: false
    t.string "managed_provider", default: "hetzner", null: false
    t.string "managed_region", default: "nbg1", null: false
    t.string "managed_size_slug", default: "cpx11", null: false
    t.string "name", null: false
    t.integer "project_id", null: false
    t.string "runtime_kind", default: "managed", null: false
    t.integer "runtime_project_id"
    t.string "service_account_email"
    t.datetime "updated_at", null: false
    t.string "workload_identity_pool", null: false
    t.string "workload_identity_provider", null: false
    t.index ["current_release_id"], name: "index_environments_on_current_release_id"
    t.index ["environment_bundle_id"], name: "index_environments_on_environment_bundle_id"
    t.index ["ingress_strategy"], name: "index_environments_on_ingress_strategy"
    t.index ["project_id"], name: "index_environments_on_project_id"
    t.index ["runtime_project_id"], name: "index_environments_on_runtime_project_id"
  end

  create_table "login_links", force: :cascade do |t|
    t.datetime "auth_code_consumed_at"
    t.string "auth_code_digest"
    t.datetime "auth_code_expires_at"
    t.string "code_challenge"
    t.string "code_challenge_method"
    t.datetime "consumed_at"
    t.datetime "created_at", null: false
    t.datetime "expires_at", null: false
    t.string "ip_address"
    t.string "redirect_path"
    t.string "redirect_uri"
    t.string "state"
    t.string "token_digest", null: false
    t.datetime "updated_at", null: false
    t.string "user_agent"
    t.integer "user_id", null: false
    t.index ["auth_code_digest"], name: "index_login_links_on_auth_code_digest", unique: true
    t.index ["expires_at"], name: "index_login_links_on_expires_at"
    t.index ["token_digest"], name: "index_login_links_on_token_digest", unique: true
    t.index ["user_id"], name: "index_login_links_on_user_id"
  end

  create_table "node_bootstrap_tokens", force: :cascade do |t|
    t.datetime "consumed_at"
    t.datetime "created_at", null: false
    t.bigint "environment_id"
    t.datetime "expires_at", null: false
    t.integer "issued_by_user_id"
    t.string "managed_provider"
    t.string "managed_region"
    t.string "managed_size_slug"
    t.integer "node_id"
    t.integer "organization_id"
    t.string "provider_server_id"
    t.string "public_ip"
    t.string "purpose", default: "manual", null: false
    t.string "token_digest", null: false
    t.datetime "updated_at", null: false
    t.index ["environment_id"], name: "index_node_bootstrap_tokens_on_environment_id"
    t.index ["expires_at"], name: "index_node_bootstrap_tokens_on_expires_at"
    t.index ["issued_by_user_id"], name: "index_node_bootstrap_tokens_on_issued_by_user_id"
    t.index ["node_id"], name: "index_node_bootstrap_tokens_on_node_id"
    t.index ["organization_id"], name: "index_node_bootstrap_tokens_on_organization_id"
    t.index ["purpose"], name: "index_node_bootstrap_tokens_on_purpose"
    t.index ["token_digest"], name: "index_node_bootstrap_tokens_on_token_digest", unique: true
  end

  create_table "node_bundles", force: :cascade do |t|
    t.datetime "claimed_at"
    t.datetime "created_at", null: false
    t.string "desired_state_object_path", default: "", null: false
    t.integer "desired_state_sequence", default: 0, null: false
    t.integer "environment_bundle_id", null: false
    t.integer "node_id"
    t.integer "organization_bundle_id", null: false
    t.datetime "provisioned_at"
    t.text "provisioning_error"
    t.integer "runtime_project_id", null: false
    t.string "status", default: "provisioning", null: false
    t.string "token", null: false
    t.datetime "updated_at", null: false
    t.index ["environment_bundle_id", "status"], name: "index_node_bundles_on_env_bundle_and_status"
    t.index ["environment_bundle_id"], name: "index_node_bundles_on_environment_bundle_id"
    t.index ["node_id"], name: "index_node_bundles_on_node_id", unique: true
    t.index ["organization_bundle_id"], name: "index_node_bundles_on_organization_bundle_id"
    t.index ["runtime_project_id"], name: "index_node_bundles_on_runtime_project_id"
    t.index ["token"], name: "index_node_bundles_on_token", unique: true
  end

  create_table "node_diagnose_requests", force: :cascade do |t|
    t.datetime "claimed_at"
    t.datetime "completed_at"
    t.datetime "created_at", null: false
    t.text "error_message"
    t.bigint "node_id", null: false
    t.datetime "requested_at", null: false
    t.bigint "requested_by_user_id", null: false
    t.text "result_json"
    t.string "status", default: "pending", null: false
    t.datetime "updated_at", null: false
    t.index ["completed_at"], name: "index_node_diagnose_requests_on_completed_at"
    t.index ["node_id", "status", "requested_at"], name: "index_node_diagnose_requests_on_claim_lookup"
    t.index ["node_id"], name: "index_node_diagnose_requests_on_node_id"
    t.index ["requested_by_user_id"], name: "index_node_diagnose_requests_on_requested_by_user_id"
  end

  create_table "nodes", force: :cascade do |t|
    t.datetime "access_expires_at"
    t.string "access_token_digest", null: false
    t.text "capabilities_json", default: "[]", null: false
    t.datetime "created_at", null: false
    t.string "desired_state_bucket", default: "", null: false
    t.string "desired_state_object_path", default: "", null: false
    t.integer "desired_state_sequence", default: 0, null: false
    t.integer "environment_id"
    t.text "ingress_tls_last_error"
    t.datetime "ingress_tls_not_after"
    t.string "ingress_tls_status", default: "", null: false
    t.text "labels_json", default: "[\"web\"]", null: false
    t.datetime "last_seen_at"
    t.datetime "lease_expires_at"
    t.boolean "managed", default: false, null: false
    t.string "managed_provider"
    t.string "managed_region"
    t.string "managed_size_slug"
    t.string "name"
    t.integer "node_bundle_id"
    t.integer "organization_id"
    t.string "provider_server_id"
    t.text "provisioning_error"
    t.string "provisioning_status", default: "failed", null: false
    t.string "public_ip"
    t.datetime "refresh_expires_at"
    t.string "refresh_token_digest", default: "", null: false
    t.datetime "revoked_at"
    t.datetime "updated_at", null: false
    t.index ["access_token_digest"], name: "index_nodes_on_access_token_digest", unique: true
    t.index ["environment_id"], name: "index_nodes_on_environment_id"
    t.index ["lease_expires_at"], name: "index_nodes_on_lease_expires_at"
    t.index ["managed", "managed_provider", "managed_region", "managed_size_slug", "organization_id", "environment_id", "revoked_at"], name: "index_nodes_on_managed_capacity_lookup"
    t.index ["node_bundle_id"], name: "index_nodes_on_node_bundle_id"
    t.index ["organization_id"], name: "index_nodes_on_organization_id"
    t.index ["refresh_token_digest"], name: "index_nodes_on_refresh_token_digest", unique: true
  end

  create_table "organization_bundles", force: :cascade do |t|
    t.datetime "claimed_at"
    t.integer "claimed_by_organization_id"
    t.datetime "created_at", null: false
    t.string "gar_repository_name", null: false
    t.string "gar_repository_region", null: false
    t.string "gar_writer_service_account_email", null: false
    t.string "gcs_bucket_name", null: false
    t.datetime "provisioned_at"
    t.text "provisioning_error"
    t.integer "runtime_project_id", null: false
    t.string "status", default: "provisioning", null: false
    t.string "token", null: false
    t.datetime "updated_at", null: false
    t.index ["claimed_by_organization_id"], name: "index_organization_bundles_on_claimed_by_organization_id"
    t.index ["gar_repository_name"], name: "index_organization_bundles_on_gar_repository_name", unique: true
    t.index ["gar_writer_service_account_email"], name: "index_org_bundles_on_writer_sa", unique: true
    t.index ["gcs_bucket_name"], name: "index_organization_bundles_on_gcs_bucket_name", unique: true
    t.index ["runtime_project_id"], name: "index_organization_bundles_on_runtime_project_id"
    t.index ["status"], name: "index_organization_bundles_on_status"
    t.index ["token"], name: "index_organization_bundles_on_token", unique: true
  end

  create_table "organization_memberships", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.integer "organization_id", null: false
    t.string "role", null: false
    t.datetime "updated_at", null: false
    t.integer "user_id", null: false
    t.index ["organization_id", "user_id"], name: "index_organization_memberships_on_organization_id_and_user_id", unique: true
    t.index ["organization_id"], name: "index_organization_memberships_on_organization_id"
    t.index ["user_id", "role"], name: "index_organization_memberships_on_user_id_and_role"
    t.index ["user_id"], name: "index_organization_memberships_on_user_id"
  end

  create_table "organization_registry_configs", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.datetime "expires_at"
    t.bigint "organization_id", null: false
    t.text "password", null: false
    t.string "registry_host", null: false
    t.string "repository_namespace", null: false
    t.datetime "updated_at", null: false
    t.string "username", null: false
    t.index ["organization_id"], name: "index_organization_registry_configs_on_organization_id", unique: true
  end

  create_table "organization_workload_identities", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.integer "created_by_user_id"
    t.string "gcp_project_id", null: false
    t.string "gcp_project_number", null: false
    t.text "last_error"
    t.integer "organization_id", null: false
    t.integer "project_id"
    t.string "service_account_email", null: false
    t.string "status", default: "failed", null: false
    t.datetime "updated_at", null: false
    t.string "workload_identity_pool", null: false
    t.string "workload_identity_provider", null: false
    t.index ["created_by_user_id"], name: "index_organization_workload_identities_on_created_by_user_id"
    t.index ["organization_id", "project_id"], name: "index_org_workload_identities_on_org_and_project", unique: true, where: "(project_id IS NOT NULL)"
    t.index ["organization_id"], name: "index_organization_workload_identities_on_organization_id"
    t.index ["project_id"], name: "index_organization_workload_identities_on_project_id"
  end

  create_table "organizations", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "gar_repository_name", default: "", null: false
    t.string "gar_repository_region", default: "", null: false
    t.string "gcp_project_id", default: "", null: false
    t.string "gcp_project_number", default: "", null: false
    t.string "gcs_bucket_name", default: "", null: false
    t.string "name", null: false
    t.integer "organization_bundle_id"
    t.string "plan_tier", default: "paid", null: false
    t.text "provisioning_error"
    t.string "provisioning_status", default: "failed", null: false
    t.integer "runtime_project_id"
    t.datetime "updated_at", null: false
    t.string "workload_identity_pool", default: "", null: false
    t.string "workload_identity_provider", default: "", null: false
    t.index ["name"], name: "index_organizations_on_name"
    t.index ["organization_bundle_id"], name: "index_organizations_on_organization_bundle_id"
    t.index ["runtime_project_id"], name: "index_organizations_on_runtime_project_id"
  end

  create_table "projects", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "name", null: false
    t.integer "organization_id", null: false
    t.datetime "updated_at", null: false
    t.index ["organization_id"], name: "index_projects_on_organization_id"
  end

  create_table "releases", force: :cascade do |t|
    t.string "command"
    t.datetime "created_at", null: false
    t.text "desired_state_json"
    t.string "desired_state_sha256"
    t.string "desired_state_uri"
    t.string "entrypoint"
    t.text "env_json", default: "{}", null: false
    t.string "git_sha", null: false
    t.integer "healthcheck_interval_seconds", default: 5, null: false
    t.string "healthcheck_path"
    t.integer "healthcheck_port"
    t.integer "healthcheck_timeout_seconds", default: 2, null: false
    t.string "image_digest", null: false
    t.string "image_repository", default: "", null: false
    t.integer "project_id", null: false
    t.datetime "published_at"
    t.string "release_command"
    t.string "revision"
    t.text "secret_refs_json", default: "[]", null: false
    t.string "status", default: "draft", null: false
    t.datetime "updated_at", null: false
    t.text "web_json", default: "{}", null: false
    t.text "worker_json", default: "{}", null: false
    t.index ["project_id", "created_at"], name: "index_releases_on_project_id_and_created_at"
    t.index ["project_id", "git_sha"], name: "index_releases_on_project_id_and_git_sha"
    t.index ["project_id"], name: "index_releases_on_project_id"
  end

  create_table "runtime_projects", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "gar_region", null: false
    t.string "gcp_project_id", null: false
    t.string "gcp_project_number", null: false
    t.string "gcs_bucket_prefix", null: false
    t.string "kind", default: "shared_sandbox", null: false
    t.string "name", null: false
    t.string "runtime_backend", default: "gcp", null: false
    t.string "slug", null: false
    t.datetime "updated_at", null: false
    t.string "workload_identity_pool", null: false
    t.string "workload_identity_provider", null: false
    t.index ["slug"], name: "index_runtime_projects_on_slug", unique: true
  end

  create_table "standalone_desired_state_documents", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.bigint "environment_id"
    t.string "etag", null: false
    t.bigint "node_bundle_id", null: false
    t.bigint "node_id", null: false
    t.text "payload_json", null: false
    t.integer "sequence", null: false
    t.string "sha256", null: false
    t.datetime "updated_at", null: false
    t.index ["environment_id"], name: "index_standalone_desired_state_documents_on_environment_id"
    t.index ["node_bundle_id", "sequence"], name: "idx_standalone_desired_state_docs_on_bundle_and_sequence", unique: true
    t.index ["node_bundle_id"], name: "index_standalone_desired_state_documents_on_node_bundle_id"
    t.index ["node_id", "sequence"], name: "idx_standalone_desired_state_docs_on_node_and_sequence", unique: true
    t.index ["node_id"], name: "index_standalone_desired_state_documents_on_node_id"
  end

  create_table "user_identities", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "email", null: false
    t.datetime "last_used_at"
    t.text "profile_json", default: "{}", null: false
    t.string "provider", null: false
    t.string "provider_uid", null: false
    t.datetime "updated_at", null: false
    t.integer "user_id", null: false
    t.index ["provider", "provider_uid"], name: "index_user_identities_on_provider_and_provider_uid", unique: true
    t.index ["user_id", "provider"], name: "index_user_identities_on_user_id_and_provider", unique: true
    t.index ["user_id"], name: "index_user_identities_on_user_id"
  end

  create_table "users", force: :cascade do |t|
    t.string "account_kind", default: "human", null: false
    t.string "anonymous_identifier"
    t.string "anonymous_secret_digest"
    t.datetime "claimed_at"
    t.datetime "confirmed_at"
    t.datetime "created_at", null: false
    t.string "email"
    t.datetime "updated_at", null: false
    t.index ["anonymous_identifier"], name: "index_users_on_anonymous_identifier", unique: true, where: "(anonymous_identifier IS NOT NULL)"
    t.index ["email"], name: "index_users_on_email", unique: true, where: "(email IS NOT NULL)"
  end

  add_foreign_key "api_tokens", "users"
  add_foreign_key "claim_links", "users"
  add_foreign_key "deployment_node_statuses", "deployments"
  add_foreign_key "deployment_node_statuses", "nodes"
  add_foreign_key "deployments", "environments"
  add_foreign_key "deployments", "nodes", column: "release_command_node_id"
  add_foreign_key "deployments", "releases"
  add_foreign_key "environment_bundles", "environments", column: "claimed_by_environment_id"
  add_foreign_key "environment_bundles", "organization_bundles"
  add_foreign_key "environment_bundles", "runtime_projects"
  add_foreign_key "environment_ingresses", "environments"
  add_foreign_key "environment_secrets", "environments"
  add_foreign_key "environments", "environment_bundles"
  add_foreign_key "environments", "projects"
  add_foreign_key "environments", "releases", column: "current_release_id"
  add_foreign_key "environments", "runtime_projects"
  add_foreign_key "login_links", "users"
  add_foreign_key "node_bootstrap_tokens", "environments"
  add_foreign_key "node_bootstrap_tokens", "nodes"
  add_foreign_key "node_bootstrap_tokens", "organizations"
  add_foreign_key "node_bootstrap_tokens", "users", column: "issued_by_user_id"
  add_foreign_key "node_bundles", "environment_bundles"
  add_foreign_key "node_bundles", "nodes"
  add_foreign_key "node_bundles", "organization_bundles"
  add_foreign_key "node_bundles", "runtime_projects"
  add_foreign_key "node_diagnose_requests", "nodes"
  add_foreign_key "node_diagnose_requests", "users", column: "requested_by_user_id"
  add_foreign_key "nodes", "environments"
  add_foreign_key "nodes", "node_bundles"
  add_foreign_key "nodes", "organizations"
  add_foreign_key "organization_bundles", "organizations", column: "claimed_by_organization_id"
  add_foreign_key "organization_bundles", "runtime_projects"
  add_foreign_key "organization_memberships", "organizations"
  add_foreign_key "organization_memberships", "users"
  add_foreign_key "organization_registry_configs", "organizations"
  add_foreign_key "organization_workload_identities", "organizations"
  add_foreign_key "organization_workload_identities", "projects"
  add_foreign_key "organization_workload_identities", "users", column: "created_by_user_id"
  add_foreign_key "organizations", "organization_bundles"
  add_foreign_key "organizations", "runtime_projects"
  add_foreign_key "projects", "organizations"
  add_foreign_key "releases", "projects"
  add_foreign_key "standalone_desired_state_documents", "environments"
  add_foreign_key "standalone_desired_state_documents", "node_bundles"
  add_foreign_key "standalone_desired_state_documents", "nodes"
  add_foreign_key "user_identities", "users"
end
