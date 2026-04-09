# frozen_string_literal: true

require "base64"
require "digest"
require "uri"

class LoginLink < ApplicationRecord
  TOKEN_BYTES = 32
  AUTH_CODE_BYTES = 32
  TTL = 15.minutes
  AUTH_CODE_TTL = 5.minutes
  LOOPBACK_HOSTS = ["127.0.0.1", "localhost"].freeze

  belongs_to :user

  validates :token_digest, presence: true
  validates :expires_at, presence: true

  def self.issue!(user:, request:, redirect_path: nil, redirect_uri: nil, state: nil, code_challenge: nil, code_challenge_method: nil)
    raw_token = SecureRandom.hex(TOKEN_BYTES)

    link = create!(
      user: user,
      token_digest: digest(raw_token),
      expires_at: TTL.from_now,
      ip_address: request.remote_ip,
      user_agent: request.user_agent,
      redirect_path: redirect_path,
      redirect_uri: redirect_uri,
      state: state,
      code_challenge: code_challenge,
      code_challenge_method: code_challenge_method
    )

    [link, raw_token]
  end

  def self.find_valid(raw_token)
    return nil if raw_token.blank?

    link = find_by(token_digest: digest(raw_token))
    return nil unless link&.active?

    link
  end

  def self.issue_cli_auth_code!(user:, request:, redirect_uri:, state:, code_challenge:, code_challenge_method:)
    link, _raw_token = issue!(
      user: user,
      request: request,
      redirect_uri: redirect_uri,
      state: state,
      code_challenge: code_challenge,
      code_challenge_method: code_challenge_method
    )
    link.consume!
    raw_code = link.issue_auth_code!
    [ link, raw_code ]
  end

  def self.find_by_auth_code(raw_code)
    return nil if raw_code.blank?

    find_by(auth_code_digest: digest(raw_code))
  end

  def self.digest(value)
    Digest::SHA256.hexdigest(value)
  end

  def self.safe_redirect_uri(value)
    uri = URI.parse(value.to_s)
    return nil unless uri.is_a?(URI::HTTP)
    return nil unless uri.scheme == "http"
    host = uri.host&.downcase
    return nil unless LOOPBACK_HOSTS.include?(host)

    uri.to_s
  rescue URI::InvalidURIError
    nil
  end

  def active?
    !expired? && !consumed?
  end

  def expired?
    expires_at <= Time.current
  end

  def consumed?
    consumed_at.present?
  end

  def consume!
    update!(consumed_at: Time.current)
  end

  def issue_auth_code!
    raw_code = SecureRandom.hex(AUTH_CODE_BYTES)
    update!(
      auth_code_digest: self.class.digest(raw_code),
      auth_code_expires_at: AUTH_CODE_TTL.from_now,
      auth_code_consumed_at: nil
    )
    raw_code
  end

  def consume_auth_code!
    update!(auth_code_consumed_at: Time.current)
  end

  def auth_code_active?
    auth_code_expires_at.present? && auth_code_expires_at > Time.current && auth_code_consumed_at.blank?
  end

  def valid_code_verifier?(verifier)
    return false if verifier.blank?
    return false if code_challenge.blank?
    return false unless code_challenge_method == "S256"

    digest = Digest::SHA256.digest(verifier)
    Base64.urlsafe_encode64(digest, padding: false) == code_challenge
  end

  def redirect_uri_with(code, state)
    uri = URI.parse(redirect_uri)
    params = Rack::Utils.parse_nested_query(uri.query)
    params["code"] = code
    params["state"] = state if state.present?
    uri.query = params.to_query.presence
    uri.to_s
  end
end
