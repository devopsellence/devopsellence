# frozen_string_literal: true

require "test_helper"

module Releases
  class RuntimeAttributesTest < ActiveSupport::TestCase
    test "raises invalid payload for non-string JSON scalar params" do
      error = assert_raises(RuntimeAttributes::InvalidPayload) do
        RuntimeAttributes.new(
          params: {
            git_sha: "a" * 40,
            image_repository: "api",
            image_digest: "sha256:#{"b" * 64}",
            services: 42
          }
        ).to_h
      end

      assert_equal "services must be valid JSON", error.message
    end
  end
end
