# frozen_string_literal: true

require "test_helper"
require "openssl"
require "securerandom"

class IngressCertificatesIssuerTest < ActiveSupport::TestCase
  test "call accepts csr subject alt name from openssl request" do
    hostname = "#{SecureRandom.hex(4)}.devopsellence.io"
    csr_pem = build_csr_pem(hostname:)
    certificate_pem = build_certificate_pem(hostname:)
    expected_contact = if Devopsellence::RuntimeConfig.current.acme_contact_email.present?
      [ "mailto:#{Devopsellence::RuntimeConfig.current.acme_contact_email}" ]
    else
      []
    end

    challenge = mock("challenge")
    challenge.stubs(:record_name).returns("_acme-challenge")
    challenge.stubs(:record_content).returns("txt-value")
    challenge.stubs(:status).returns("valid")
    challenge.stubs(:request_validation)
    challenge.stubs(:reload)

    authorization = mock("authorization")
    authorization.stubs(:dns).returns(challenge)

    order = mock("order")
    order.stubs(:authorizations).returns([ authorization ])
    order.stubs(:finalize)
    order.stubs(:status).returns("valid")
    order.stubs(:certificate_url).returns("https://acme.test/cert")
    order.stubs(:reload)
    order.stubs(:certificate).returns(certificate_pem)

    client = mock("acme_client")
    client.expects(:new_account).with(contact: expected_contact, terms_of_service_agreed: true)
    client.stubs(:new_order).with(identifiers: [ hostname ]).returns(order)

    cloudflare_client = mock("cloudflare_client")
    cloudflare_client.expects(:replace_dns_txt_records).with(
      hostname: "_acme-challenge.#{hostname}",
      values: [ "txt-value" ]
    )
    cloudflare_client.expects(:delete_dns_records).with(
      hostname: "_acme-challenge.#{hostname}",
      type: "TXT"
    )

    result = IngressCertificates::Issuer.new(
      hostname:,
      csr_pem:,
      client:,
      cloudflare_client:,
      dns_resolver: ->(_) { [ "txt-value" ] }
    ).call

    assert_equal certificate_pem, result.certificate_pem
    assert_operator result.not_after, :>, Time.current
  end

  test "csr dns names includes subject alt name from extReq attribute" do
    hostname = "#{SecureRandom.hex(4)}.devopsellence.io"
    csr = OpenSSL::X509::Request.new(build_csr_pem(hostname:))

    issuer = IngressCertificates::Issuer.new(
      hostname:,
      csr_pem: csr.to_pem,
      client: mock("acme_client"),
      cloudflare_client: mock("cloudflare_client")
    )

    assert_includes issuer.send(:csr_dns_names, csr), hostname
  end

  test "call waits for dns txt propagation before requesting validation" do
    hostname = "#{SecureRandom.hex(4)}.devopsellence.io"
    csr_pem = build_csr_pem(hostname:)
    certificate_pem = build_certificate_pem(hostname:)

    challenge = mock("challenge")
    challenge.stubs(:record_name).returns("_acme-challenge")
    challenge.stubs(:record_content).returns("txt-value")
    challenge.stubs(:status).returns("valid")
    challenge.expects(:request_validation).once
    challenge.stubs(:reload)

    authorization = mock("authorization")
    authorization.stubs(:dns).returns(challenge)

    order = mock("order")
    order.stubs(:authorizations).returns([ authorization ])
    order.stubs(:finalize)
    order.stubs(:status).returns("valid")
    order.stubs(:certificate_url).returns("https://acme.test/cert")
    order.stubs(:reload)
    order.stubs(:certificate).returns(certificate_pem)

    client = mock("acme_client")
    client.stubs(:new_account)
    client.stubs(:new_order).returns(order)

    cloudflare_client = mock("cloudflare_client")
    cloudflare_client.stubs(:replace_dns_txt_records)
    cloudflare_client.stubs(:delete_dns_records)

    resolver_calls = 0
    issuer = IngressCertificates::Issuer.new(
      hostname:,
      csr_pem:,
      client:,
      cloudflare_client:,
      dns_resolver: lambda do |_record_hostname|
        resolver_calls += 1
        resolver_calls >= 2 ? [ "txt-value" ] : []
      end
    )
    issuer.stubs(:sleep)

    issuer.call

    assert_operator resolver_calls, :>=, 2
  end

  test "call fails when dns txt record does not propagate before timeout" do
    hostname = "#{SecureRandom.hex(4)}.devopsellence.io"
    csr_pem = build_csr_pem(hostname:)
    now = Time.current
    clock_values = [
      now,
      now,
      now + IngressCertificates::Issuer::TXT_PROPAGATION_TIMEOUT + 1.second
    ]

    challenge = mock("challenge")
    challenge.stubs(:record_name).returns("_acme-challenge")
    challenge.stubs(:record_content).returns("txt-value")
    challenge.stubs(:request_validation)

    authorization = mock("authorization")
    authorization.stubs(:dns).returns(challenge)

    order = mock("order")
    order.stubs(:authorizations).returns([ authorization ])

    client = mock("acme_client")
    client.stubs(:new_account)
    client.stubs(:new_order).returns(order)

    cloudflare_client = mock("cloudflare_client")
    cloudflare_client.stubs(:replace_dns_txt_records)
    cloudflare_client.stubs(:delete_dns_records)

    issuer = IngressCertificates::Issuer.new(
      hostname:,
      csr_pem:,
      client:,
      cloudflare_client:,
      clock: -> { clock_values.shift || clock_values.last || now },
      dns_resolver: ->(_) { [] }
    )
    issuer.stubs(:sleep)

    error = assert_raises(RuntimeError) { issuer.call }

    assert_match(/did not propagate/, error.message)
  end

  private
    def build_csr_pem(hostname:)
      key = OpenSSL::PKey::RSA.new(2048)
      csr = OpenSSL::X509::Request.new
      csr.version = 0
      csr.subject = OpenSSL::X509::Name.parse("/CN=#{hostname}")
      csr.public_key = key.public_key

      extension = OpenSSL::X509::ExtensionFactory.new.create_extension("subjectAltName", "DNS:#{hostname}")
      attribute = OpenSSL::X509::Attribute.new(
        "extReq",
        OpenSSL::ASN1::Set([ OpenSSL::ASN1::Sequence([ extension ]) ])
      )
      csr.add_attribute(attribute)
      csr.sign(key, OpenSSL::Digest::SHA256.new)
      csr.to_pem
    end

    def build_certificate_pem(hostname:)
      key = OpenSSL::PKey::RSA.new(2048)
      cert = OpenSSL::X509::Certificate.new
      cert.version = 2
      cert.serial = SecureRandom.random_number(10_000)
      cert.subject = OpenSSL::X509::Name.parse("/CN=#{hostname}")
      cert.issuer = cert.subject
      cert.public_key = key.public_key
      cert.not_before = Time.current - 60
      cert.not_after = Time.current + 1.day
      cert.sign(key, OpenSSL::Digest::SHA256.new)
      cert.to_pem
    end
end
