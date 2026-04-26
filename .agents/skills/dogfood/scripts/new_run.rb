#!/usr/bin/env ruby
# frozen_string_literal: true

require "fileutils"
require "open3"
require "optparse"
require "time"

options = {
  root: Dir.pwd,
  out: "/tmp/devopsellence-dogfood"
}

parser = OptionParser.new do |opts|
  opts.banner = "Usage: new_run.rb SCENARIO_SLUG [--root PATH] [--out PATH]"
  opts.on("--root PATH", "Repository root used for git metadata") { |value| options[:root] = value }
  opts.on("--out PATH", "Parent output directory") { |value| options[:out] = value }
end

begin
  parser.parse!
rescue OptionParser::ParseError => e
  warn e.message
  warn parser
  exit 64
end

scenario = ARGV.shift
unless scenario
  warn parser
  exit 64
end

slug = scenario.downcase.gsub(/[^a-z0-9]+/, "-").gsub(/\A-|-+\z/, "")
if slug.empty?
  warn "SCENARIO_SLUG must contain at least one lowercase letter or digit after normalization"
  warn parser
  exit 64
end

now = Time.now.utc
timestamp = now.strftime("%Y%m%dT%H%M%S%6NZ")
run_dir = File.expand_path("#{timestamp}-#{slug}", options[:out])

def git_value(root, *args)
  stdout, status = Open3.capture2("git", *args, chdir: root)
  status.success? ? stdout.strip : "unknown"
rescue SystemCallError
  "unknown"
end

commit = git_value(options[:root], "rev-parse", "--short", "HEAD")
branch = git_value(options[:root], "branch", "--show-current")
branch = "detached" if branch.empty?

template_path = File.expand_path("../references/report-template.md", __dir__)
template = File.read(template_path)
report = template
  .sub("Scenario:", "Scenario: #{scenario}")
  .sub("Date:", "Date: #{now.iso8601}")
  .sub("Commit:", "Commit: #{commit} (#{branch})")
  .sub("Run path:", "Run path: #{run_dir}")

FileUtils.mkdir_p(options[:out])
begin
  Dir.mkdir(run_dir)
rescue Errno::EEXIST
  warn "run directory already exists: #{run_dir}"
  exit 73
end
File.write(File.join(run_dir, "report.md"), report)
File.write(File.join(run_dir, "commands.log"), "# Commands for #{scenario}\n")

puts run_dir
