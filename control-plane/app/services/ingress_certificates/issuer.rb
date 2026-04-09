# frozen_string_literal: true

require "acme/client"
require "fileutils"
require "openssl"
require "resolv"

module IngressCertificates
  class Issuer
    CHALLENGE_TIMEOUT = 60.seconds
    ORDER_TIMEOUT = 60.seconds
    TXT_PROPAGATION_TIMEOUT = 30.seconds
    POLL_INTERVAL = 2.seconds

    Result = Struct.new(:certificate_pem, :not_after, keyword_init: true)

    def initialize(hostname:, csr_pem:, client: nil, cloudflare_client: Cloudflare::RestClient.new, clock: nil, dns_resolver: nil)
      @hostname = hostname.to_s.strip
      @csr_pem = csr_pem.to_s
      @client = client
      @cloudflare_client = cloudflare_client
      @clock = clock || -> { Time.current }
      @dns_resolver = dns_resolver || method(:resolve_txt_records)
    end

    def call
      raise ArgumentError, "hostname is required" if hostname.blank?
      raise ArgumentError, "csr is required" if csr_pem.blank?

      csr = OpenSSL::X509::Request.new(csr_pem)
      validate_csr!(csr)

      order = acme_client.new_order(identifiers: [ hostname ])
      authorization = order.authorizations.first || raise("missing authorization for #{hostname}")
      challenge = authorization.dns
      challenge_hostname = full_challenge_hostname(challenge.record_name)

      cloudflare_client.replace_dns_txt_records(
        hostname: challenge_hostname,
        values: [ challenge.record_content ]
      )

      wait_for_txt_record!(challenge_hostname, challenge.record_content)
      challenge.request_validation
      wait_for_challenge!(challenge)

      order.finalize(csr: csr)
      certificate_pem = wait_for_certificate!(order)
      not_after = OpenSSL::X509::Certificate.new(certificate_pem).not_after

      Result.new(certificate_pem:, not_after:)
    ensure
      if defined?(challenge_hostname) && challenge_hostname.present?
        cloudflare_client.delete_dns_records(hostname: challenge_hostname, type: "TXT")
      end
    end

    private

    attr_reader :hostname, :csr_pem, :cloudflare_client, :clock, :dns_resolver

    def validate_csr!(csr)
      names = csr_dns_names(csr)
      raise "csr must include #{hostname}" unless names.include?(hostname)
    end

    def csr_dns_names(csr)
      names = []
      names << csr.subject.to_a.find { |name, _, _| name == "CN" }&.[](1)
      csr.attributes.each do |attribute|
        next unless attribute.oid == "extReq"

        names.concat(csr_attribute_dns_names(attribute))
      end

      names.map(&:to_s).map(&:strip).reject(&:blank?).uniq
    end

    def csr_attribute_dns_names(attribute)
      Array(attribute.value.value).flat_map do |outer_sequence|
        Array(outer_sequence.value).flat_map do |extension|
          csr_extension_dns_names(extension)
        end
      end
    end

    def csr_extension_dns_names(extension)
      return [] unless extension.is_a?(OpenSSL::ASN1::Sequence)

      oid, value = Array(extension.value)
      return [] unless oid.is_a?(OpenSSL::ASN1::ObjectId)
      return [] unless oid.value == "subjectAltName"

      san_sequence = OpenSSL::ASN1.decode(value.value)
      Array(san_sequence.value).filter_map do |entry|
        next unless entry.tag_class == :CONTEXT_SPECIFIC
        next unless entry.tag == 2

        entry.value.to_s
      end
    rescue OpenSSL::ASN1::ASN1Error
      []
    end

    def wait_for_challenge!(challenge)
      deadline = clock.call + CHALLENGE_TIMEOUT
      while challenge.status == "pending" && clock.call < deadline
        sleep POLL_INTERVAL
        challenge.reload
      end
      return if challenge.status == "valid"

      raise "dns challenge failed for #{hostname}: #{challenge.status}"
    end

    def wait_for_txt_record!(record_hostname, expected_value)
      deadline = clock.call + TXT_PROPAGATION_TIMEOUT

      loop do
        values = Array(dns_resolver.call(record_hostname)).map(&:to_s)
        return if values.include?(expected_value)

        raise "dns challenge record did not propagate for #{hostname}" if clock.call >= deadline

        sleep POLL_INTERVAL
      end
    end

    def wait_for_certificate!(order)
      deadline = clock.call + ORDER_TIMEOUT
      while order.status == "processing" && clock.call < deadline
        sleep POLL_INTERVAL
        order.reload
      end
      order.reload if order.status == "valid" && order.certificate_url.blank?
      return order.certificate if order.certificate_url.present?

      raise "certificate issuance failed for #{hostname}: #{order.status}"
    end

    def acme_client
      @acme_client ||= begin
        client = @client || Acme::Client.new(
          private_key: account_key,
          directory: runtime.acme_directory_url
        )
        payload = {
          contact: [],
          terms_of_service_agreed: true
        }
        payload[:contact] = [ "mailto:#{runtime.acme_contact_email}" ] if runtime.acme_contact_email.present?
        client.new_account(**payload)
        client
      end
    end

    def account_key
      @account_key ||= begin
        path = runtime.acme_account_key_path.to_s
        FileUtils.mkdir_p(File.dirname(path))
        if File.exist?(path)
          OpenSSL::PKey::RSA.new(File.read(path))
        else
          key = OpenSSL::PKey::RSA.new(4096)
          File.write(path, key.to_pem)
          File.chmod(0o600, path)
          key
        end
      end
    end

    def full_challenge_hostname(record_name)
      record_name = record_name.to_s.sub(/\.\z/, "")
      return record_name if record_name.blank?
      return record_name if record_name == hostname || record_name.end_with?(".#{hostname}")

      [ record_name, hostname ].join(".")
    end

    def resolve_txt_records(record_hostname)
      Resolv::DNS.open(nameserver_port: [["1.1.1.1", 53], ["1.0.0.1", 53]]) do |dns|
        dns.getresources(record_hostname, Resolv::DNS::Resource::IN::TXT).map do |resource|
          Array(resource.data).join
        end
      end
    rescue Resolv::ResolvError, SocketError
      []
    end

    def runtime
      Devopsellence::RuntimeConfig.current
    end
  end
end
