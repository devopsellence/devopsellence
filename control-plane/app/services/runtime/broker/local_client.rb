# frozen_string_literal: true

require "base64"
require "erb"
require "googleauth"
require "json"
require "net/http"
require "time"

module Runtime
  module Broker
    class LocalClient
      IAM_SCOPE = "https://www.googleapis.com/auth/cloud-platform"
      GAR_TOKEN_SCOPE = "https://www.googleapis.com/auth/cloud-platform"
      GAR_TOKEN_LIFETIME = "1200s"
      SERVICE_ACCOUNT_READINESS_RETRIES = 20
      SERVICE_ACCOUNT_IAM_FETCH_RETRIES = 20
      SERVICE_ACCOUNT_IAM_UPDATE_RETRIES = 20
      GAR_TOKEN_READINESS_RETRIES = 90
      REPOSITORY_READINESS_RETRIES = 30
      BUCKET_IAM_FETCH_RETRIES = 20
      BUCKET_IAM_UPDATE_RETRIES = 20
      REPOSITORY_IAM_FETCH_RETRIES = 20
      REPOSITORY_IAM_UPDATE_RETRIES = 20
      SECRET_IAM_FETCH_RETRIES = 5
      SECRET_IAM_UPDATE_RETRIES = 20

      Result = Struct.new(:status, :message, keyword_init: true)
      PushAuth = Struct.new(
        :registry_host,
        :gar_repository_path,
        :docker_username,
        :docker_password,
        :expires_in,
        keyword_init: true
      )

      def initialize(client: nil, iam: nil, retry_sleep_seconds: 1)
        @client = client || Gcp::RestClient.new
        @iam = iam
        @retry_sleep_seconds = retry_sleep_seconds
      end

      def ensure_environment_runtime!(environment:)
        service_account_email = environment.service_account_email.to_s.strip
        raise "environment has no service account (missing bundle?)" if service_account_email.blank?

        member = service_account_member(service_account_email)
        ensure_bucket_role_binding!(
          bucket_name: environment.project.organization.gcs_bucket_name,
          role: "roles/storage.objectViewer",
          member: member
        )
        organization = environment.project.organization
        ensure_repository_role_binding_for!(
          project_id: organization.gcp_project_id,
          region: organization.gar_repository_region,
          repository_name: organization.gar_repository_name,
          role: "roles/artifactregistry.reader",
          member: member
        )
        Result.new(status: :ready, message: nil)
      rescue StandardError => error
        Result.new(status: :failed, message: utf8(error.message))
      end

      def ensure_node_runtime!(node:)
        bundle = node.node_bundle
        raise "node has no bundle" unless bundle

        node.update!(
          desired_state_bucket: bundle.desired_state_bucket,
          desired_state_object_path: bundle.desired_state_object_path,
          desired_state_sequence: [ node.desired_state_sequence, bundle.desired_state_sequence ].max,
          provisioning_status: Node::PROVISIONING_READY,
          provisioning_error: nil
        )
        Result.new(status: Node::PROVISIONING_READY, message: nil)
      rescue StandardError => error
        node.update!(
          provisioning_status: Node::PROVISIONING_FAILED,
          provisioning_error: "runtime broker provisioning failed: #{utf8(error.message)}"
        )
        Result.new(status: Node::PROVISIONING_FAILED, message: node.provisioning_error)
      end

      def upsert_environment_secret!(environment_secret:, value:)
        secret_value = value.to_s
        raise ArgumentError, "secret value is required" if secret_value.blank?

        value_sha256 = EnvironmentSecret.value_sha256(secret_value)
        service_account_email = environment_secret.environment.service_account_email.to_s.strip
        access_verified = environment_secret.access_verified_for?(service_account_email)
        environment_secret.save!
        unless access_verified
          ensure_environment_runtime_ready!(environment_secret.environment)
          ensure_secret_exists!(environment: environment_secret.environment, gcp_secret_name: environment_secret.gcp_secret_name)
        end
        if environment_secret.value_sha256 != value_sha256
          ensure_environment_runtime_ready!(environment_secret.environment) if access_verified
          ensure_secret_exists!(environment: environment_secret.environment, gcp_secret_name: environment_secret.gcp_secret_name)
          add_secret_version!(environment: environment_secret.environment, gcp_secret_name: environment_secret.gcp_secret_name, value: secret_value)
        end
        ensure_environment_secret_access!(environment_secret:) unless access_verified

        updates = {}
        updates[:value_sha256] = value_sha256 if environment_secret.value_sha256 != value_sha256
        if environment_secret.access_grantee_email != service_account_email
          updates[:access_grantee_email] = service_account_email
        end
        if environment_secret.access_verified_at.blank?
          updates[:access_verified_at] = Time.current
        end
        environment_secret.update_columns(updates) if updates.any?
        environment_secret
      end

      def destroy_environment_secret!(environment_secret:)
        delete_secret_if_present!(
          environment: environment_secret.environment,
          gcp_secret_name: environment_secret.gcp_secret_name
        )
        environment_secret.destroy!
        environment_secret
      end

      def ensure_environment_secret_access!(environment_secret:)
        service_account_email = environment_secret.environment.service_account_email.to_s.strip
        return if service_account_email.blank?

        ensure_environment_runtime_ready!(environment_secret.environment)
        ensure_secret_access!(
          environment: environment_secret.environment,
          gcp_secret_name: environment_secret.gcp_secret_name,
          service_account_email: service_account_email
        )
        environment_secret.update_columns(
          access_grantee_email: service_account_email,
          access_verified_at: Time.current
        )
      end

      def provision_organization_bundle!(bundle:)
        ensure_bucket_name!(
          project_id: bundle.runtime_project.gcp_project_id,
          bucket_name: bundle.gcs_bucket_name
        )
        ensure_repository_named!(
          project_id: bundle.runtime_project.gcp_project_id,
          region: bundle.gar_repository_region,
          repository_name: bundle.gar_repository_name,
          description: "devopsellence org bundle #{bundle.token}"
        )
        ensure_service_account!(
          project_id: bundle.runtime_project.gcp_project_id,
          service_account_email: bundle.gar_writer_service_account_email,
          display_name: "devopsellence org bundle #{bundle.token} GAR push writer"
        )
        ensure_service_account_token_creator!(
          service_account_email: bundle.gar_writer_service_account_email,
          member: control_plane_runtime_service_account_member_for_project(bundle.runtime_project.gcp_project_id)
        )
        ensure_repository_role_binding_for!(
          project_id: bundle.runtime_project.gcp_project_id,
          region: bundle.gar_repository_region,
          repository_name: bundle.gar_repository_name,
          role: "roles/artifactregistry.writer",
          member: service_account_member(bundle.gar_writer_service_account_email)
        )
        Result.new(status: :ready, message: nil)
      rescue StandardError => error
        Result.new(status: :failed, message: utf8(error.message))
      end

      def provision_environment_bundle!(bundle:)
        ensure_service_account!(
          project_id: bundle.runtime_project.gcp_project_id,
          service_account_email: bundle.service_account_email,
          display_name: "devopsellence env bundle #{bundle.token}"
        )
        ensure_bucket_role_binding!(
          bucket_name: bundle.organization_bundle.gcs_bucket_name,
          role: "roles/storage.objectViewer",
          member: service_account_member(bundle.service_account_email)
        )
        ensure_repository_role_binding_for!(
          project_id: bundle.runtime_project.gcp_project_id,
          region: bundle.organization_bundle.gar_repository_region,
          repository_name: bundle.organization_bundle.gar_repository_name,
          role: "roles/artifactregistry.reader",
          member: service_account_member(bundle.service_account_email)
        )
        Result.new(status: :ready, message: nil)
      rescue StandardError => error
        Result.new(status: :failed, message: utf8(error.message))
      end

      def ensure_node_bundle_impersonation!(bundle:)
        resource = service_account_resource_name_from(
          project_id: bundle.runtime_project.gcp_project_id,
          service_account_email: bundle.environment_bundle.service_account_email
        )
        policy = fetch_service_account_policy(resource)
        policy["bindings"] ||= []

        member = node_bundle_member(bundle:)
        binding = policy["bindings"].find { |entry| entry["role"] == "roles/iam.workloadIdentityUser" }
        return Result.new(status: :ready, message: nil) if Array(binding&.fetch("members", [])).include?(member)

        if binding
          binding["members"] = (Array(binding["members"]) + [ member ]).uniq
        else
          policy["bindings"] << { "role" => "roles/iam.workloadIdentityUser", "members" => [ member ] }
        end

        set_service_account_policy(resource, policy)
        Result.new(status: :ready, message: nil)
      rescue StandardError => error
        Result.new(status: :failed, message: utf8(error.message))
      end

      def revoke_node_bundle_impersonation!(bundle:)
        resource = service_account_resource_name_from(
          project_id: bundle.runtime_project.gcp_project_id,
          service_account_email: bundle.environment_bundle.service_account_email
        )
        policy = fetch_service_account_policy(resource)
        policy["bindings"] ||= []

        member = node_bundle_member(bundle:)
        binding = policy["bindings"].find { |entry| entry["role"] == "roles/iam.workloadIdentityUser" }
        return Result.new(status: :ready, message: nil) unless binding

        binding["members"] = Array(binding["members"]).reject { |m| m == member }
        policy["bindings"] = policy["bindings"].reject { |entry| entry["role"] == "roles/iam.workloadIdentityUser" && Array(entry["members"]).empty? }

        set_service_account_policy(resource, policy)
        Result.new(status: :ready, message: nil)
      rescue StandardError => error
        Result.new(status: :failed, message: utf8(error.message))
      end

      def issue_gar_push_auth!(organization:)
        bundle = organization.organization_bundle
        raise "organization has no bundle" unless bundle

        ensure_service_account_token_creator!(
          service_account_email: bundle.gar_writer_service_account_email,
          member: control_plane_runtime_service_account_member_for_project(bundle.runtime_project.gcp_project_id)
        )
        ensure_repository_role_binding_for!(
          project_id: bundle.runtime_project.gcp_project_id,
          region: bundle.gar_repository_region,
          repository_name: bundle.gar_repository_name,
          role: "roles/artifactregistry.writer",
          member: service_account_member(bundle.gar_writer_service_account_email)
        )

        response = generate_service_account_access_token(bundle.gar_writer_service_account_email)
        raise "gar push token generation failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

        body = JSON.parse(response.body.presence || "{}")
        PushAuth.new(
          registry_host: organization.gar_repository_path.split("/").first,
          gar_repository_path: organization.gar_repository_path,
          docker_username: "oauth2accesstoken",
          docker_password: body.fetch("accessToken"),
          expires_in: expiry_delta_seconds(body["expireTime"])
        )
      end

      private

      attr_reader :client, :retry_sleep_seconds

      def ensure_bucket_name!(project_id:, bucket_name:)
        uri = "#{Gcp::Endpoints.storage_api_base}/storage/v1/b?project=#{project_id}"
        response = client.post(uri, payload: { name: bucket_name })
        return if response.code.to_i.between?(200, 299) || response.code.to_i == 409

        raise "bucket create failed (#{response.code}): #{utf8(response.body)}"
      end


      def ensure_repository_named!(project_id:, region:, repository_name:, description:)
        repository_id = ERB::Util.url_encode(repository_name)
        uri = [
          "#{Gcp::Endpoints.artifact_registry_base}/projects",
          project_id,
          "locations",
          region,
          "repositories?repositoryId=#{repository_id}"
        ].join("/")
        response = client.post(uri, payload: { format: "DOCKER", description: description })
        if response.code.to_i.between?(200, 299) || response.code.to_i == 409
          wait_for_repository_ready!(project_id:, region:, repository_name:)
          return
        end

        raise "gar repo create failed (#{response.code}): #{utf8(response.body)}"
      end


      def wait_for_repository_ready!(organization: nil, project_id: nil, region: nil, repository_name: nil)
        project_id ||= organization.gcp_project_id
        region ||= organization.gar_repository_region
        repository_name ||= organization.gar_repository_name
        REPOSITORY_READINESS_RETRIES.times do |attempt|
          response = client.get(repository_resource_uri_from(project_id:, region:, repository_name:))
          return if response.code.to_i.between?(200, 299)
          raise "gar repo readiness check failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i == 404

          sleep retry_sleep_seconds if attempt < REPOSITORY_READINESS_RETRIES - 1
        end

        raise "gar repo not ready after #{REPOSITORY_READINESS_RETRIES} attempts"
      end

      def generate_service_account_access_token(service_account_email)
        payload = {
          scope: [ GAR_TOKEN_SCOPE ],
          lifetime: GAR_TOKEN_LIFETIME
        }
        uri = "#{iam_credentials_service_account_uri(service_account_email)}:generateAccessToken"
        response = nil

        GAR_TOKEN_READINESS_RETRIES.times do |attempt|
          response = client.post(uri, payload:)
          return response if response.code.to_i.between?(200, 299)
          break unless gar_token_retryable_response?(response)

          sleep retry_sleep_seconds if attempt < GAR_TOKEN_READINESS_RETRIES - 1
        end

        response
      end

      def ensure_service_account!(project_id:, service_account_email:, display_name:)
        response = client.get(service_account_uri(project_id:, service_account_email:))
        return if response.code.to_i.between?(200, 299)
        raise "service account fetch failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i == 404

        response = client.post(
          "#{Gcp::Endpoints.iam_base}/projects/#{project_id}/serviceAccounts",
          payload: {
            accountId: service_account_email.split("@").first,
            serviceAccount: { displayName: display_name }
          }
        )
        raise "service account create failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299) || response.code.to_i == 409
        wait_for_service_account_readiness!(project_id:, service_account_email:)
      end

      def wait_for_service_account_readiness!(project_id:, service_account_email:)
        SERVICE_ACCOUNT_READINESS_RETRIES.times do |attempt|
          response = client.get(service_account_uri(project_id:, service_account_email:))
          return if response.code.to_i.between?(200, 299)
          raise "service account readiness check failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i == 404

          raise "service account not ready after #{SERVICE_ACCOUNT_READINESS_RETRIES} attempts" if attempt >= SERVICE_ACCOUNT_READINESS_RETRIES - 1

          sleep retry_sleep_seconds
        end
      end

      def ensure_service_account_token_creator!(service_account_email:, member:)
        resource = "projects/-/serviceAccounts/#{service_account_email}"
        policy = fetch_service_account_policy(resource)
        policy["bindings"] ||= []

        binding = policy["bindings"].find { |entry| entry["role"] == "roles/iam.serviceAccountTokenCreator" }
        members = Array(binding&.fetch("members", []))
        return if members.include?(member)

        if binding
          binding["members"] = (members + [ member ]).uniq
        else
          policy["bindings"] << { "role" => "roles/iam.serviceAccountTokenCreator", "members" => [ member ] }
        end

        set_service_account_policy(resource, policy)
      end

      def ensure_bucket_role_binding!(bucket_name:, role:, member:)
        policy_uri = "#{Gcp::Endpoints.storage_api_base}/storage/v1/b/#{ERB::Util.url_encode(bucket_name)}/iam"
        response = nil
        BUCKET_IAM_UPDATE_RETRIES.times do |attempt|
          response = fetch_bucket_policy(policy_uri)
          policy = JSON.parse(response.body)
          policy["bindings"] ||= []
          prune_deleted_members!(policy["bindings"])
          return if policy_binding_has_member?(policy["bindings"], role, member)
          upsert_member!(policy["bindings"], role, member)

          response = client.put(policy_uri, payload: policy)
          break if response.code.to_i.between?(200, 299)
          next if bucket_iam_conflict_response?(response) && attempt < BUCKET_IAM_UPDATE_RETRIES - 1
          break unless propagation_retryable_response?(response)

          sleep retry_sleep_seconds if attempt < BUCKET_IAM_UPDATE_RETRIES - 1
        end
        raise "bucket iam update failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)
      end

      def ensure_repository_role_binding_for!(project_id:, region:, repository_name:, role:, member:)
        response = nil
        REPOSITORY_IAM_FETCH_RETRIES.times do |attempt|
          response = client.get("#{repository_resource_uri_from(project_id:, region:, repository_name:)}:getIamPolicy")
          break if response.code.to_i.between?(200, 299)
          break unless response.code.to_i == 404

          sleep retry_sleep_seconds if attempt < REPOSITORY_IAM_FETCH_RETRIES - 1
        end
        raise "repo iam fetch failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)

        policy = JSON.parse(response.body)
        policy["bindings"] ||= []
        return if policy_binding_has_member?(policy["bindings"], role, member)
        upsert_member!(policy["bindings"], role, member)

        response = nil
        REPOSITORY_IAM_UPDATE_RETRIES.times do |attempt|
          response = client.post("#{repository_resource_uri_from(project_id:, region:, repository_name:)}:setIamPolicy", payload: { policy: policy })
          break if response.code.to_i.between?(200, 299)
          break unless response.code.to_i == 400

          sleep retry_sleep_seconds if attempt < REPOSITORY_IAM_UPDATE_RETRIES - 1
        end
        raise "repo iam update failed (#{response.code}): #{utf8(response.body)}" unless response.code.to_i.between?(200, 299)
      end

      def ensure_secret_exists!(environment:, gcp_secret_name:)
        response = client.post(
          "#{Gcp::Endpoints.secret_manager_base}/projects/#{environment.gcp_project_id}/secrets?secretId=#{gcp_secret_name}",
          payload: { replication: { automatic: {} } }
        )
        return if response.code.to_i.between?(200, 299) || response.code.to_i == 409

        raise "secret create failed (#{response.code}): #{utf8(response.body)}"
      end

      def add_secret_version!(environment:, gcp_secret_name:, value:)
        response = client.post(
          "#{secret_base_uri(environment:, gcp_secret_name:)}:addVersion",
          payload: { payload: { data: Base64.strict_encode64(value) } }
        )
        return if response.code.to_i.between?(200, 299)

        raise "secret version add failed (#{response.code}): #{utf8(response.body)}"
      end

      def ensure_secret_access!(environment:, gcp_secret_name:, service_account_email:)
        service_account_email = service_account_email.to_s.strip
        return if service_account_email.blank?

        policy = fetch_secret_policy(environment:, gcp_secret_name:)
        member = service_account_member(service_account_email)
        binding = policy.fetch("bindings", []).find { |entry| entry["role"] == "roles/secretmanager.secretAccessor" }
        members = Array(binding&.fetch("members", []))
        return if members.include?(member)

        if binding
          binding["members"] = (members + [ member ]).uniq
        else
          policy["bindings"] = Array(policy["bindings"]) + [
            { "role" => "roles/secretmanager.secretAccessor", "members" => [ member ] }
          ]
        end

        response = nil
        SECRET_IAM_UPDATE_RETRIES.times do |attempt|
          response = client.post("#{secret_base_uri(environment:, gcp_secret_name:)}:setIamPolicy", payload: { policy: policy })
          return if response.code.to_i.between?(200, 299)
          break unless propagation_retryable_response?(response)

          sleep retry_sleep_seconds if attempt < SECRET_IAM_UPDATE_RETRIES - 1
        end

        raise "secret iam update failed (#{response.code}): #{utf8(response.body)}"
      end

      def fetch_secret_policy(environment:, gcp_secret_name:)
        response = nil
        SECRET_IAM_FETCH_RETRIES.times do |attempt|
          response = client.get("#{secret_base_uri(environment:, gcp_secret_name:)}:getIamPolicy")
          return JSON.parse(response.body.presence || "{}") if response.code.to_i.between?(200, 299)
          break unless response.code.to_i == 404

          sleep retry_sleep_seconds if attempt < SECRET_IAM_FETCH_RETRIES - 1
        end

        raise "secret iam fetch failed (#{response.code}): #{utf8(response.body)}"
      end

      def delete_secret_if_present!(environment:, gcp_secret_name:)
        response = client.delete(secret_base_uri(environment:, gcp_secret_name:))
        return if response.code.to_i.between?(200, 299) || response.code.to_i == 404

        raise "secret delete failed (#{response.code}): #{utf8(response.body)}"
      end

      def ensure_environment_runtime_ready!(environment)
        result = ensure_environment_runtime!(environment:)
        raise result.message unless result.status == :ready
      end


      def repository_resource_uri(organization)
        repository_resource_uri_from(
          project_id: organization.gcp_project_id,
          region: organization.gar_repository_region,
          repository_name: organization.gar_repository_name
        )
      end

      def repository_resource_uri_from(project_id:, region:, repository_name:)
        [
          "#{Gcp::Endpoints.artifact_registry_base}/projects",
          project_id,
          "locations",
          region,
          "repositories",
          repository_name
        ].join("/")
      end

      def secret_base_uri(environment:, gcp_secret_name:)
        "#{Gcp::Endpoints.secret_manager_base}/projects/#{environment.gcp_project_id}/secrets/#{gcp_secret_name}"
      end

      def control_plane_runtime_service_account_member(organization)
        service_account_member(control_plane_runtime_service_account_email(organization))
      end

      def control_plane_runtime_service_account_member_for_project(project_id)
        service_account_member(control_plane_runtime_service_account_email_for_project(project_id))
      end

      def control_plane_runtime_service_account_email(organization)
        control_plane_runtime_service_account_email_for_project(organization.gcp_project_id)
      end

      def control_plane_runtime_service_account_email_for_project(project_id)
        runtime = Devopsellence::RuntimeConfig.current
        configured_email = runtime.control_plane_service_account_email.to_s.strip
        return configured_email if configured_email.present?

        discovered_email = current_service_account_email
        return discovered_email if discovered_email.present?

        control_plane_project_id = runtime.control_plane_service_account_project_id.to_s.strip
        control_plane_project_id = project_id if control_plane_project_id.blank?
        account_id = runtime.control_plane_service_account_id.to_s.strip
        "#{account_id}@#{control_plane_project_id.presence || project_id}.iam.gserviceaccount.com"
      end

      def current_service_account_email
        return @current_service_account_email if defined?(@current_service_account_email)

        metadata_host = ENV.fetch("GCE_METADATA_HOST", "metadata.google.internal").to_s.strip
        uri = URI::HTTP.build(host: metadata_host, path: "/computeMetadata/v1/instance/service-accounts/default/email")
        request = Net::HTTP::Get.new(uri)
        request["Metadata-Flavor"] = "Google"

        response = Net::HTTP.start(uri.host, uri.port, open_timeout: 1, read_timeout: 1) do |http|
          http.request(request)
        end

        @current_service_account_email = response.is_a?(Net::HTTPSuccess) ? response.body.to_s.strip.presence : nil
      rescue StandardError
        @current_service_account_email = nil
      end

      def service_account_resource_name(environment)
        "projects/#{environment.gcp_project_id}/serviceAccounts/#{environment.service_account_email}"
      end

      def service_account_resource_name_from(project_id:, service_account_email:)
        "projects/#{project_id}/serviceAccounts/#{service_account_email}"
      end

      def service_account_member(service_account_email)
        "serviceAccount:#{service_account_email}"
      end

      def iam_credentials_service_account_uri(service_account_email)
        "#{Gcp::Endpoints.iam_credentials_base}/projects/-/serviceAccounts/#{ERB::Util.url_encode(service_account_email)}"
      end

      def upsert_member!(bindings, role, member)
        binding = bindings.find { |entry| entry["role"] == role }
        if binding
          binding["members"] ||= []
          binding["members"] << member unless binding["members"].include?(member)
        else
          bindings << { "role" => role, "members" => [ member ] }
        end
      end

      def prune_deleted_members!(bindings)
        Array(bindings).each do |binding|
          binding["members"] = Array(binding["members"]).reject { |member| deleted_member?(member) }
        end
        bindings.reject! { |binding| Array(binding["members"]).empty? }
      end

      def deleted_member?(member)
        member.to_s.start_with?("deleted:")
      end

      def propagation_retryable_response?(response)
        return false unless [ 400, 404 ].include?(response.code.to_i)

        utf8(response.body).match?(/service account .* does not exist|unknown service account/i)
      end

      def fetch_bucket_policy(policy_uri)
        response = nil
        BUCKET_IAM_FETCH_RETRIES.times do |attempt|
          response = client.get(policy_uri)
          return response if response.code.to_i.between?(200, 299)
          break unless propagation_retryable_response?(response)

          sleep retry_sleep_seconds if attempt < BUCKET_IAM_FETCH_RETRIES - 1
        end

        raise "bucket iam fetch failed (#{response.code}): #{utf8(response.body)}"
      end

      def bucket_iam_conflict_response?(response)
        return true if response.code.to_i == 412

        utf8(response.body).match?(/conditionNotMet|If-Match/i)
      end

      def gar_token_retryable_response?(response)
        return true if response.code.to_i == 404
        return false unless response.code.to_i == 403

        utf8(response.body).match?(/iam_permission_denied|iam\.serviceAccounts\.getAccessToken|permission.*getAccessToken/i)
      end

      def policy_binding_has_member?(bindings, role, member)
        binding = Array(bindings).find { |entry| entry["role"] == role }
        Array(binding&.fetch("members", [])).include?(member)
      end

      def expiry_delta_seconds(expire_time)
        expiry = Time.iso8601(expire_time.to_s)
        [ (expiry - Time.current).to_i, 0 ].max
      rescue ArgumentError
        0
      end

      def service_account_uri(project_id:, service_account_email:)
        "#{Gcp::Endpoints.iam_base}/projects/#{project_id}/serviceAccounts/#{ERB::Util.url_encode(service_account_email)}"
      end

      def fetch_service_account_policy(resource)
        response = nil
        SERVICE_ACCOUNT_IAM_FETCH_RETRIES.times do |attempt|
          response = client.post("#{Gcp::Endpoints.iam_base}/#{resource}:getIamPolicy", payload: {})
          return JSON.parse(response.body.presence || "{}") if response.code.to_i.between?(200, 299)
          break unless service_account_iam_retryable_response?(response)

          sleep retry_sleep_seconds if attempt < SERVICE_ACCOUNT_IAM_FETCH_RETRIES - 1
        end

        raise "service account iam fetch failed (#{response.code}): #{utf8(response.body)}"
      end

      def set_service_account_policy(resource, policy)
        response = nil
        SERVICE_ACCOUNT_IAM_UPDATE_RETRIES.times do |attempt|
          response = client.post("#{Gcp::Endpoints.iam_base}/#{resource}:setIamPolicy", payload: { policy: policy })
          return if response.code.to_i.between?(200, 299)
          break unless service_account_iam_retryable_response?(response)

          sleep retry_sleep_seconds if attempt < SERVICE_ACCOUNT_IAM_UPDATE_RETRIES - 1
        end

        raise "service account iam update failed (#{response.code}): #{utf8(response.body)}"
      end

      def service_account_iam_retryable_response?(response)
        return true if propagation_retryable_response?(response)
        return false unless response.code.to_i == 403

        utf8(response.body).match?(/iam_permission_denied|permission.*getiampolicy|permission.*setiampolicy/i)
      end

      def utf8(value)
        value.to_s.encode("UTF-8", invalid: :replace, undef: :replace, replace: "?")
      end

      def node_bundle_member(bundle:)
        runtime = bundle.runtime_project
        "principal://iam.googleapis.com/#{runtime.workload_identity_pool_resource_name}/subject/node_bundle:#{bundle.token}"
      end
    end
  end
end
