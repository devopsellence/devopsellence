# frozen_string_literal: true

require "test_helper"
require "securerandom"

class DashboardOrganizationsTest < ActionDispatch::IntegrationTest
  self.use_transactional_tests = false
  include ActiveJob::TestHelper

  setup do
    clear_enqueued_jobs
    clear_performed_jobs
  end

  test "dashboard organization routes redirect to getting started without provisioning" do
    user = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    sign_in_as(user)

    assert_no_difference("Organization.count") do
      post dashboard_organizations_path, params: { name: "acme" }
    end
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")
    assert_enqueued_jobs 0

    organization = Organization.create!(name: "acme")
    OrganizationMembership.create!(organization: organization, user: user, role: "owner")

    assert_no_difference("NodeBootstrapToken.count") do
      post dashboard_bootstrap_node_path(organization)
    end
    assert_redirected_to getting_started_path(anchor: "quickstart-heading")
  end

  test "dashboard bootstrap route redirects contributors to getting started" do
    owner = User.create!(email: "owner-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    contributor = User.create!(email: "contrib-#{SecureRandom.hex(4)}@example.com", confirmed_at: Time.current)
    organization = Organization.create!(name: "shared-org")
    OrganizationMembership.create!(organization: organization, user: owner, role: "owner")
    OrganizationMembership.create!(organization: organization, user: contributor, role: "contributor")

    sign_in_as(contributor)

    assert_no_difference("NodeBootstrapToken.count") do
      post dashboard_bootstrap_node_path(organization)
    end

    assert_redirected_to getting_started_path(anchor: "quickstart-heading")
  end

  private

  def sign_in_as(user)
    raw_token = SecureRandom.hex(16)
    LoginLink.create!(
      user: user,
      token_digest: LoginLink.digest(raw_token),
      expires_at: 15.minutes.from_now
    )

    without_http_basic do
      get login_verify_path(token: raw_token)
      assert_redirected_to getting_started_path(anchor: "quickstart-heading")
    end
  end
end
