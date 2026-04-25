# frozen_string_literal: true

require "test_helper"

module NodeDesiredState
  class IngressPayloadTest < ActiveSupport::TestCase
    test "rejects explicit null redirect_http instead of emitting null desired state" do
      release = Struct.new(:ingress_config).new({ "redirect_http" => nil })

      error = assert_raises(Release::InvalidRuntimeConfig) do
        IngressPayload.configured_redirect_http(release)
      end

      assert_equal "ingress.redirect_http must be a boolean", error.message
    end
  end
end
