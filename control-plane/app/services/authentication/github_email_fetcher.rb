# frozen_string_literal: true

require "json"
require "net/http"
require "uri"

module Authentication
  class GithubEmailFetcher
    EMAILS_URI = URI("https://api.github.com/user/emails")

    def call(token:)
      token = token.to_s.strip
      return nil if token.blank?

      request = Net::HTTP::Get.new(EMAILS_URI)
      request["Authorization"] = "Bearer #{token}"
      request["Accept"] = "application/vnd.github+json"
      request["User-Agent"] = "devopsellence"

      response = Net::HTTP.start(EMAILS_URI.host, EMAILS_URI.port, use_ssl: true) do |http|
        http.request(request)
      end
      return nil unless response.code.to_i.between?(200, 299)

      emails = JSON.parse(response.body)
      preferred = emails.find { |entry| entry["verified"] && entry["primary"] } ||
        emails.find { |entry| entry["verified"] }
      preferred&.fetch("email", nil)&.to_s&.strip&.downcase
    rescue JSON::ParserError
      nil
    end
  end
end
