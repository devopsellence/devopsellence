# frozen_string_literal: true

module Gcp
  module Endpoints
    module_function

    def storage_api_base
      configured(:gcs_endpoint, "DEVOPSELLENCE_GCS_ENDPOINT", "https://storage.googleapis.com")
    end

    def secret_manager_base
      configured(:secret_manager_endpoint, "DEVOPSELLENCE_SECRET_MANAGER_ENDPOINT", "https://secretmanager.googleapis.com/v1")
    end

    def sts_token_url
      configured(:sts_endpoint, "DEVOPSELLENCE_STS_ENDPOINT", "https://sts.googleapis.com/v1/token")
    end

    def artifact_registry_base
      configured(:artifact_registry_endpoint, "DEVOPSELLENCE_ARTIFACT_REGISTRY_ENDPOINT", "https://artifactregistry.googleapis.com/v1")
    end

    def artifact_registry_download_base
      configured(:artifact_registry_download_endpoint, "DEVOPSELLENCE_ARTIFACT_REGISTRY_DOWNLOAD_ENDPOINT", "https://artifactregistry.googleapis.com/download/v1")
    end

    def iam_base
      configured(:iam_endpoint, "DEVOPSELLENCE_IAM_ENDPOINT", "https://iam.googleapis.com/v1")
    end

    def iam_credentials_base
      configured(:iam_credentials_endpoint, "DEVOPSELLENCE_IAM_CREDENTIALS_ENDPOINT", "https://iamcredentials.googleapis.com/v1")
    end

    def cloud_kms_base
      configured(:cloud_kms_endpoint, "DEVOPSELLENCE_CLOUD_KMS_ENDPOINT", "https://cloudkms.googleapis.com/v1")
    end

    def configured(runtime_key, env_key, fallback)
      value = runtime&.public_send(runtime_key).to_s.strip
      value = ENV.fetch(env_key, "").to_s.strip if value.blank?
      value = fallback if value.blank?
      value.sub(%r{/*$}, "")
    end

    def runtime
      return unless defined?(Devopsellence::RuntimeConfig)

      Devopsellence::RuntimeConfig.current
    rescue StandardError
      nil
    end

    module_function :configured, :runtime
    private_class_method :configured, :runtime
  end
end
