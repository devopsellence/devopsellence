# frozen_string_literal: true

require "test_helper"

class EnvironmentTest < ActiveSupport::TestCase
  test "name is unique within a project" do
    organization = Organization.create!(name: "org-#{SecureRandom.hex(3)}")
    project = organization.projects.create!(name: "demo")
    project.environments.create!(name: "production")

    duplicate = project.environments.new(name: "production")

    assert_not duplicate.valid?
    assert_includes duplicate.errors[:name], "has already been taken"
  end
end
