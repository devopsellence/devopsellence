# frozen_string_literal: true

require "test_helper"
require "base64"
require "json"
require "openssl"

class IdpSubjectTokenIssuerTest < ActiveSupport::TestCase
  PooledIdentity = Struct.new(
    :audience,
    :gcp_project_id,
    :gcp_project_number,
    :service_account_email,
    keyword_init: true
  )

  test "issues token for pooled managed identity without environment-only fields" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    node, = issue_test_node!(organization: organization, name: "node-a")
    identity = PooledIdentity.new(
      audience: "//iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/pool/providers/provider",
      gcp_project_id: "devopsellence-test",
      gcp_project_number: "123456789",
      service_account_email: "node-a@devopsellence-test.iam.gserviceaccount.com"
    )

    with_env("DEVOPSELLENCE_IDP_PRIVATE_KEY_PEM" => OpenSSL::PKey::RSA.generate(2048).to_pem) do
      token = Idp::SubjectTokenIssuer.issue!(
        node: node,
        environment: identity,
        issuer: "https://dev.test.devopsellence.com"
      )

      payload = JSON.parse(Base64.urlsafe_decode64(token.split(".")[1]))

      assert_equal node.id.to_s, payload["node_id"]
      assert_equal "", payload["project_id"]
      assert_equal "", payload["environment_id"]
      assert_equal "0", payload["identity_version"]
      assert_equal identity.gcp_project_id, payload["gcp_project_id"]
      assert_equal identity.service_account_email, payload["service_account_email"]
      assert_equal "", payload["organization_bundle_token"]
      assert_equal "", payload["environment_bundle_token"]
      assert_equal "", payload["node_bundle_token"]
    end
  end
end
