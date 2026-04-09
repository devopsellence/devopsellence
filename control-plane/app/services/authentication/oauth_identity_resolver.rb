# frozen_string_literal: true

module Authentication
  class OauthIdentityResolver
    class Error < StandardError; end
    class MissingVerifiedEmailError < Error; end
    class IdentityConflictError < Error; end
    class UnsupportedProviderError < Error; end

    def initialize(auth_hash:, github_email_fetcher: GithubEmailFetcher.new)
      @auth_hash = auth_hash
      @github_email_fetcher = github_email_fetcher
    end

    def call
      provider = normalized_provider
      provider_uid = auth_hash.fetch("uid").to_s

      identity = UserIdentity.find_by(provider: provider, provider_uid: provider_uid)
      if identity
        identity.update!(last_used_at: Time.current, profile: profile_payload(identity.email))
        return identity.user
      end

      email = resolved_email
      raise MissingVerifiedEmailError, provider_error_message(provider) if email.blank?

      user = User.find_or_initialize_by(email: email)
      user.confirm! if user.persisted? && user.confirmed_at.nil?
      if user.new_record?
        user.confirmed_at = Time.current
        user.save!
      end

      begin
        user.user_identities.create!(
          provider: provider,
          provider_uid: provider_uid,
          email: email,
          profile: profile_payload(email),
          last_used_at: Time.current
        )
      rescue ActiveRecord::RecordNotUnique, ActiveRecord::RecordInvalid => error
        raise IdentityConflictError, "This #{provider.titleize} account is already linked to another user." if conflict_error?(error)

        raise
      end

      user
    end

    private

    attr_reader :auth_hash, :github_email_fetcher

    def normalized_provider
      provider = auth_hash.fetch("provider").to_s
      return "google" if provider == "google_oauth2"
      return "github" if provider == "github"

      raise UnsupportedProviderError, "Unsupported OAuth provider: #{provider}"
    end

    def resolved_email
      case normalized_provider
      when "google"
        info = auth_hash.fetch("info", {})
        extra = auth_hash.fetch("extra", {})
        raw_info = extra.fetch("raw_info", {})
        return info["email"].to_s.strip.downcase if raw_info["email_verified"]
      when "github"
        token = auth_hash.dig("credentials", "token").to_s
        return github_email_fetcher.call(token: token)
      end

      nil
    end

    def provider_error_message(provider)
      case provider
      when "google"
        "Google did not provide a verified email. Try GitHub sign-in instead or contact contact@devopsellence.com."
      when "github"
        "GitHub did not provide a verified email. Try Google sign-in instead or contact contact@devopsellence.com."
      else
        "Provider did not provide a verified email."
      end
    end

    def profile_payload(email)
      info = auth_hash.fetch("info", {})
      {
        "email" => email,
        "name" => info["name"],
        "nickname" => info["nickname"],
        "image" => info["image"]
      }.compact
    end

    def conflict_error?(error)
      return true if error.is_a?(ActiveRecord::RecordNotUnique)

      record = error.respond_to?(:record) ? error.record : nil
      return false unless record

      record.errors.of_kind?(:provider, :taken) || record.errors.of_kind?(:provider_uid, :taken)
    end
  end
end
