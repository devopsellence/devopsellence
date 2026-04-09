# frozen_string_literal: true

require "test_helper"

class ProjectTest < ActiveSupport::TestCase
  test "name is unique within an organization" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    organization.projects.create!(name: "demo")

    duplicate = organization.projects.new(name: "demo")

    assert_not duplicate.valid?
    assert_includes duplicate.errors[:name], "has already been taken"
  end
end
