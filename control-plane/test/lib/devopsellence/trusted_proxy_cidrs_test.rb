# frozen_string_literal: true

require "test_helper"

module Devopsellence
  class TrustedProxyCidrsTest < ActiveSupport::TestCase
    test "load returns empty array when file is missing" do
      path = Pathname.new(Dir.mktmpdir).join("missing.txt")

      assert_equal [], TrustedProxyCidrs.load(path:)
    end

    test "load parses present cidrs and skips blank lines" do
      dir = Dir.mktmpdir
      path = Pathname.new(dir).join("cidrs.txt")
      path.write("10.0.0.0/8\n\n192.168.0.0/16\n")

      cidrs = TrustedProxyCidrs.load(path:)

      assert_equal [IPAddr.new("10.0.0.0/8"), IPAddr.new("192.168.0.0/16")], cidrs
    ensure
      FileUtils.remove_entry(dir) if dir
    end
  end
end
