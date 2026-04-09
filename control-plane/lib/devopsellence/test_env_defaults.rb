# frozen_string_literal: true

module Devopsellence
  module TestEnvDefaults
    ENV = {
      "DEVOPSELLENCE_RUNTIME_BACKEND" => "gcp",
      "DEVOPSELLENCE_DEFAULT_GCP_PROJECT_ID" => "devopsellence-test",
      "DEVOPSELLENCE_DEFAULT_GCP_PROJECT_NUMBER" => "123456789",
      "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_POOL" => "projects/123456789/locations/global/workloadIdentityPools/devopsellence-test-pool",
      "DEVOPSELLENCE_DEFAULT_WORKLOAD_IDENTITY_PROVIDER" => "projects/123456789/locations/global/workloadIdentityPools/devopsellence-test-pool/providers/devopsellence-test-provider",
      "DEVOPSELLENCE_DEFAULT_GAR_REGION" => "us-east1",
      "DEVOPSELLENCE_GCS_BUCKET_PREFIX" => "devopsellence-test",
      "DEVOPSELLENCE_PUBLIC_BASE_URL" => "https://dev.test.devopsellence.com",
      "DEVOPSELLENCE_SIGNING_BACKEND" => "local",
      "DEVOPSELLENCE_HTTP_BASIC_USERNAME" => "",
      "DEVOPSELLENCE_HTTP_BASIC_PASSWORD" => "",
      "GOOGLE_CLIENT_ID" => "google-client-id",
      "GOOGLE_CLIENT_SECRET" => "google-client-secret",
      "GITHUB_CLIENT_ID" => "github-client-id",
      "GITHUB_CLIENT_SECRET" => "github-client-secret",
      "DEVOPSELLENCE_DIGITALOCEAN_DEFAULT_IMAGE" => "ubuntu-24-04-x64"
    }.freeze
  end
end
