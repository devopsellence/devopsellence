# frozen_string_literal: true

require "erb"
require "google/apis/iam_v1"
require "json"
require "set"
require "time"

module Maintenance
  class PruneZombieGcpResources
    IAM_SCOPE = Google::Apis::IamV1::AUTH_CLOUD_PLATFORM
    HTTP_RETRY_LIMIT = 5
    IAM_RETRY_LIMIT = 5
    RETRYABLE_HTTP_CODES = [ 429, 500, 502, 503, 504 ].freeze
    RETRYABLE_IAM_CODES = [ 429, 500, 502, 503, 504 ].freeze
    ACTIVE_BUNDLE_STATUSES = [
      OrganizationBundle::STATUS_PROVISIONING,
      OrganizationBundle::STATUS_WARM,
      OrganizationBundle::STATUS_CLAIMED
    ].freeze

    ProjectContext = Struct.new(
      :gcp_project_id,
      :bucket_prefixes,
      :gar_regions,
      :live_bucket_names,
      :live_repository_names,
      :live_secret_names,
      :live_service_account_emails,
      :live_node_bundle_tokens,
      keyword_init: true
    )

    Result = Struct.new(
      :deleted_buckets,
      :deleted_bucket_objects,
      :deleted_repositories,
      :deleted_secrets,
      :deleted_service_accounts,
      keyword_init: true
    ) do
      def total_deleted
        deleted_buckets + deleted_bucket_objects + deleted_repositories + deleted_secrets + deleted_service_accounts
      end
    end

    def initialize(runtime_projects: RuntimeProject.all, client: nil, iam: nil, logger: Rails.logger, sleeper: nil)
      @runtime_projects = runtime_projects
      @client = client || Gcp::RestClient.new
      @iam = iam
      @logger = logger
      @sleeper = sleeper || ->(seconds) { sleep(seconds) if seconds.to_f.positive? }
    end

    def call
      RuntimeProject.default!

      result = Result.new(
        deleted_buckets: 0,
        deleted_bucket_objects: 0,
        deleted_repositories: 0,
        deleted_secrets: 0,
        deleted_service_accounts: 0
      )

      project_contexts.each do |context|
        prune_project!(context, result)
      end

      result
    end

    private
      attr_reader :runtime_projects, :client, :logger, :sleeper

      def prune_project!(context, result)
        prune_secrets!(refresh_project_context(context), result)
        prune_service_accounts!(refresh_project_context(context), result)
        prune_repositories!(refresh_project_context(context), result)
        prune_buckets!(refresh_project_context(context), result)
      end

      def prune_secrets!(context, result)
        each_collection_item(
          "https://secretmanager.googleapis.com/v1/projects/#{context.gcp_project_id}/secrets",
          collection_key: "secrets"
        ) do |secret|
          secret_name = secret.fetch("name").to_s.split("/").last
          next unless managed_secret_name?(secret_name)
          next if context.live_secret_names.include?(secret_name)

          delete_uri(secret_uri(context.gcp_project_id, secret_name), missing_ok: true, error_prefix: "secret delete failed")
          logger.info("[prune_zombie_gcp_resources] deleted secret project=#{context.gcp_project_id} secret=#{secret_name}")
          result.deleted_secrets += 1
        end
      end

      def prune_service_accounts!(context, result)
        page_token = nil

        loop do
          response = with_iam_retry("list service accounts", project_id: context.gcp_project_id) do
            iam_service.list_project_service_accounts("projects/#{context.gcp_project_id}", page_token:)
          end

          Array(response.accounts).each do |account|
            email = account.email.to_s
            next unless managed_service_account_email?(context.gcp_project_id, email)
            next if context.live_service_account_emails.include?(email)

            with_iam_retry("delete service account", project_id: context.gcp_project_id, resource_name: email) do
              iam_service.delete_project_service_account("projects/#{context.gcp_project_id}/serviceAccounts/#{email}")
            end
            logger.info("[prune_zombie_gcp_resources] deleted service account project=#{context.gcp_project_id} service_account=#{email}")
            result.deleted_service_accounts += 1
          rescue Google::Apis::ClientError => error
            raise unless error.status_code.to_i == 404
          end

          page_token = response.next_page_token.to_s
          break if page_token.blank?
        end
      end

      def prune_repositories!(context, result)
        context.gar_regions.each do |region|
          each_collection_item(
            "https://artifactregistry.googleapis.com/v1/projects/#{context.gcp_project_id}/locations/#{region}/repositories",
            collection_key: "repositories"
          ) do |repository|
            repository_name = repository.fetch("name").to_s.split("/").last
            next unless managed_repository_name?(repository_name)
            next if context.live_repository_names.include?(repository_name)

            delete_uri(repository_uri(context.gcp_project_id, region, repository_name), missing_ok: true, error_prefix: "gar repo delete failed")
            logger.info("[prune_zombie_gcp_resources] deleted repository project=#{context.gcp_project_id} region=#{region} repository=#{repository_name}")
            result.deleted_repositories += 1
          end
        end
      end

      def prune_buckets!(context, result)
        each_collection_item(
          "https://storage.googleapis.com/storage/v1/b?project=#{context.gcp_project_id}",
          collection_key: "items"
        ) do |bucket|
          bucket_name = bucket.fetch("name").to_s
          next unless managed_bucket_name?(context.bucket_prefixes, bucket_name)

          if context.live_bucket_names.include?(bucket_name)
            prune_bucket_objects!(context, bucket_name, result)
          else
            result.deleted_bucket_objects += delete_bucket_objects(bucket_name)
            delete_uri(bucket_uri(bucket_name), missing_ok: true, error_prefix: "bucket delete failed")
            logger.info("[prune_zombie_gcp_resources] deleted bucket project=#{context.gcp_project_id} bucket=#{bucket_name}")
            result.deleted_buckets += 1
          end
        end
      end

      def prune_bucket_objects!(context, bucket_name, result)
        each_collection_item(bucket_objects_uri(bucket_name, prefix: "node-bundles/"), collection_key: "items") do |item|
          object_name = item.fetch("name").to_s
          token = extract_node_bundle_token(object_name)
          next if token.blank?
          next if context.live_node_bundle_tokens.include?(token)

          delete_uri(bucket_object_uri(bucket_name, object_name), missing_ok: true, error_prefix: "bucket object delete failed")
          logger.info("[prune_zombie_gcp_resources] deleted orphan node bundle object bucket=#{bucket_name} object=#{object_name}")
          result.deleted_bucket_objects += 1
        end
      end

      def delete_bucket_objects(bucket_name)
        deleted = 0

        each_collection_item(bucket_objects_uri(bucket_name), collection_key: "items") do |item|
          object_name = item.fetch("name").to_s
          delete_uri(bucket_object_uri(bucket_name, object_name), missing_ok: true, error_prefix: "bucket object delete failed")
          logger.info("[prune_zombie_gcp_resources] deleted bucket object bucket=#{bucket_name} object=#{object_name}")
          deleted += 1
        end

        deleted
      end

      def each_collection_item(base_uri, collection_key:)
        page_token = nil

        loop do
          uri = if page_token.present?
            "#{base_uri}#{base_uri.include?("?") ? "&" : "?"}pageToken=#{ERB::Util.url_encode(page_token)}"
          else
            base_uri
          end
          response = request_with_retry(:get, uri)
          raise "#{collection_key} list failed (#{response.code}): #{response.body}" unless response.code.to_i.between?(200, 299)

          body = JSON.parse(response.body.presence || "{}")
          Array(body[collection_key]).each { |item| yield item }

          page_token = body["nextPageToken"].to_s
          break if page_token.blank?
        end
      end

      def delete_uri(uri, missing_ok:, error_prefix:)
        response = request_with_retry(:delete, uri)
        return if response.code.to_i.between?(200, 299)
        return if missing_ok && response.code.to_i == 404

        raise "#{error_prefix} (#{response.code}): #{response.body}"
      end

      def project_contexts
        runtime_projects_by_gcp_project_id
          .map { |project_id, projects| build_project_context(project_id, projects) }
      end

      def refresh_project_context(context)
        build_project_context(context.gcp_project_id, runtime_projects_by_gcp_project_id.fetch(context.gcp_project_id, []))
      end

      def runtime_projects_by_gcp_project_id
        @runtime_projects_by_gcp_project_id ||= runtime_projects.to_a.group_by(&:gcp_project_id)
      end

      def build_project_context(project_id, projects)
        bucket_prefixes = projects.map(&:gcs_bucket_prefix).compact_blank.to_set
        live_bucket_names = live_bucket_names(project_id, bucket_prefixes)

        ProjectContext.new(
          gcp_project_id: project_id,
          bucket_prefixes: bucket_prefixes,
          gar_regions: projects.map(&:gar_region).compact_blank.to_set,
          live_bucket_names: live_bucket_names,
          live_repository_names: live_repository_names(project_id),
          live_secret_names: live_secret_names(project_id),
          live_service_account_emails: live_service_account_emails(project_id),
          live_node_bundle_tokens: live_node_bundle_tokens(project_id, bucket_prefixes, live_bucket_names)
        )
      end

      def live_bucket_names(project_id, bucket_prefixes)
        node_bucket_names = Node.where.not(desired_state_bucket: [ nil, "" ]).pluck(:desired_state_bucket).select do |bucket_name|
          managed_bucket_name?(bucket_prefixes, bucket_name)
        end

        Set.new(
          node_bucket_names +
            Organization.where(gcp_project_id: project_id).where.not(gcs_bucket_name: [ nil, "" ]).pluck(:gcs_bucket_name) +
            OrganizationBundle.joins(:runtime_project)
              .where(runtime_projects: { gcp_project_id: project_id }, status: ACTIVE_BUNDLE_STATUSES)
              .pluck(:gcs_bucket_name)
        )
      end

      def live_repository_names(project_id)
        Set.new(
          Organization.where(gcp_project_id: project_id).where.not(gar_repository_name: [ nil, "" ]).pluck(:gar_repository_name) +
            OrganizationBundle.joins(:runtime_project)
              .where(runtime_projects: { gcp_project_id: project_id }, status: ACTIVE_BUNDLE_STATUSES)
              .pluck(:gar_repository_name)
        )
      end

      def live_secret_names(project_id)
        Set.new(
          EnvironmentSecret.joins(:environment).where(environments: { gcp_project_id: project_id }).pluck(:gcp_secret_name) +
            EnvironmentIngress.joins(:environment).where(environments: { gcp_project_id: project_id }).pluck(:gcp_secret_name) +
            EnvironmentBundle.joins(:runtime_project)
              .where(runtime_projects: { gcp_project_id: project_id }, status: ACTIVE_BUNDLE_STATUSES)
              .pluck(:gcp_secret_name)
        )
      end

      def live_service_account_emails(project_id)
        Set.new(
          Environment.where(gcp_project_id: project_id).where.not(service_account_email: [ nil, "" ]).pluck(:service_account_email) +
            EnvironmentBundle.joins(:runtime_project)
              .where(runtime_projects: { gcp_project_id: project_id }, status: ACTIVE_BUNDLE_STATUSES)
              .pluck(:service_account_email) +
            OrganizationBundle.joins(:runtime_project)
              .where(runtime_projects: { gcp_project_id: project_id }, status: ACTIVE_BUNDLE_STATUSES)
              .pluck(:gar_writer_service_account_email)
        )
      end

      def live_node_bundle_tokens(project_id, bucket_prefixes, live_bucket_names)
        tokens = NodeBundle.joins(:runtime_project)
          .where(runtime_projects: { gcp_project_id: project_id }, status: ACTIVE_BUNDLE_STATUSES)
          .pluck(:token)

        node_entries = Node.where.not(desired_state_object_path: [ nil, "" ]).pluck(:desired_state_bucket, :desired_state_object_path)
        node_entries.each do |bucket_name, path|
          next unless live_bucket_names.include?(bucket_name) || managed_bucket_name?(bucket_prefixes, bucket_name)

          token = extract_node_bundle_token(path)
          tokens << token if token.present?
        end

        Set.new(tokens)
      end

      def managed_bucket_name?(bucket_prefixes, bucket_name)
        bucket_prefixes.any? do |prefix|
          bucket_name.start_with?("#{prefix}-ob-")
        end
      end

      def managed_repository_name?(repository_name)
        repository_name.match?(/\Aob-[a-z0-9]+-apps\z/)
      end

      def managed_secret_name?(secret_name)
        secret_name.match?(/\Aeb-[a-z0-9]+-ingress-cloudflare-tunnel-token\z/) ||
          secret_name.match?(/\Aenv-[a-z0-9]+-[a-z0-9-]+-[a-z0-9-]+\z/)
      end

      def managed_service_account_email?(project_id, email)
        suffix = "@#{project_id}.iam.gserviceaccount.com"
        return false unless email.end_with?(suffix)

        local_part = email.delete_suffix(suffix)
        local_part.match?(/\Aeb[a-z0-9]+\z/) || local_part.match?(/\Aob[a-z0-9]+garpush\z/)
      end

      def extract_node_bundle_token(object_path)
        match = object_path.to_s.match(%r{\Anode-bundles/([a-z0-9]+)/})
        if match
          match[1]
        else
          nil
        end
      end

      def secret_uri(project_id, secret_name)
        "https://secretmanager.googleapis.com/v1/projects/#{project_id}/secrets/#{secret_name}"
      end

      def repository_uri(project_id, region, repository_name)
        [
          "https://artifactregistry.googleapis.com/v1/projects",
          project_id,
          "locations",
          region,
          "repositories",
          repository_name
        ].join("/")
      end

      def bucket_uri(bucket_name)
        "https://storage.googleapis.com/storage/v1/b/#{ERB::Util.url_encode(bucket_name)}"
      end

      def bucket_objects_uri(bucket_name, prefix: nil)
        uri = "https://storage.googleapis.com/storage/v1/b/#{ERB::Util.url_encode(bucket_name)}/o"
        return uri if prefix.blank?

        "#{uri}?prefix=#{ERB::Util.url_encode(prefix)}"
      end

      def bucket_object_uri(bucket_name, object_name)
        "https://storage.googleapis.com/storage/v1/b/#{ERB::Util.url_encode(bucket_name)}/o/#{ERB::Util.url_encode(object_name)}"
      end

      def iam_service
        return @iam if defined?(@iam) && @iam

        @iam = Google::Apis::IamV1::IamService.new
        @iam.authorization = Gcp::Credentials.new(scope: IAM_SCOPE)
        @iam
      end

      def request_with_retry(verb, uri)
        attempts = 0

        loop do
          response = client.public_send(verb, uri)
          return response unless retryable_http_response?(response)
          raise "gcp #{verb} #{uri} rate limited after #{attempts + 1} attempts" if attempts >= HTTP_RETRY_LIMIT - 1

          attempts += 1
          sleep_with_backoff(retry_delay_seconds(response, attempt: attempts), source: "gcp_http", verb: verb, uri: uri, attempt: attempts)
        end
      end

      def with_iam_retry(action, project_id:, resource_name: nil)
        attempts = 0

        loop do
          return yield
        rescue Google::Apis::ClientError, Google::Apis::ServerError => error
          raise unless retryable_iam_error?(error)
          raise if attempts >= IAM_RETRY_LIMIT - 1

          attempts += 1
          sleep_with_backoff(retry_delay_seconds(error, attempt: attempts), source: "gcp_iam", action: action, project_id: project_id, resource_name: resource_name, attempt: attempts)
        end
      end

      def retryable_http_response?(response)
        RETRYABLE_HTTP_CODES.include?(response.code.to_i)
      end

      def retryable_iam_error?(error)
        RETRYABLE_IAM_CODES.include?(error.status_code.to_i)
      end

      def retry_delay_seconds(source, attempt:)
        retry_after = retry_after_seconds(source)
        return retry_after if retry_after.positive?

        [ attempt, 30 ].min
      end

      def retry_after_seconds(source)
        value = if source.respond_to?(:header)
          source.header["retry-after"]
        elsif source.respond_to?(:response_header)
          source.response_header["retry-after"]
        end
        return 0 if value.blank?

        parse_retry_after(value)
      end

      def parse_retry_after(value)
        seconds = value.to_s.to_i
        return seconds if seconds.positive?

        target = Time.httpdate(value.to_s)
        [ (target - Time.current).ceil, 0 ].max
      rescue ArgumentError
        0
      end

      def sleep_with_backoff(seconds, source:, **context)
        logger.warn("[prune_zombie_gcp_resources] backing off source=#{source} sleep_seconds=#{seconds} context=#{context.compact}") if logger.respond_to?(:warn)
        sleeper.call(seconds)
      end
  end
end
