#!/usr/bin/env ruby
# frozen_string_literal: true

require "fileutils"
require "open3"
require "optparse"
require "time"
require "tmpdir"

options = {
  root: Dir.pwd,
  out: File.join(Dir.tmpdir, "devopsellence-dogfood")
}

parser = OptionParser.new do |opts|
  opts.banner = "Usage: new_run.rb SCENARIO [--root PATH] [--out PATH]"
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

options[:out] = File.expand_path(options[:out])

scenario = ARGV.shift
unless scenario
  warn parser
  exit 64
end
scenario = scenario.strip
if scenario.empty?
  warn "SCENARIO must not be empty"
  warn parser
  exit 64
end
if scenario.match?(/[[:cntrl:]]/)
  warn "SCENARIO must not contain control characters"
  warn parser
  exit 64
end

slug = scenario.downcase.gsub(/[^a-z0-9]+/, "-").gsub(/\A-|-+\z/, "")
if slug.empty?
  warn "SCENARIO must contain at least one letter or digit after normalization"
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

def exit_filesystem_error(action, path, error)
  warn "#{action} #{path}: #{error.message}"
  exit 73
end

commit = git_value(options[:root], "rev-parse", "--short", "HEAD")
branch = git_value(options[:root], "branch", "--show-current")
branch = "detached" if branch.empty?

template_path = File.expand_path("../references/report-template.md", __dir__)
begin
  template = File.read(template_path)
rescue SystemCallError => e
  exit_filesystem_error("failed to read", template_path, e)
end
report = template
  .sub("Scenario:", "Scenario: #{scenario}")
  .sub("Date:", "Date: #{now.iso8601}")
  .sub("Commit:", "Commit: #{commit} (#{branch})")
  .sub("Run path:", "Run path: #{run_dir}")

begin
  FileUtils.mkdir_p(options[:out])
rescue SystemCallError => e
  exit_filesystem_error("failed to create output directory", options[:out], e)
end
begin
  Dir.mkdir(run_dir)
rescue Errno::EEXIST
  warn "run directory already exists: #{run_dir}"
  exit 73
rescue SystemCallError => e
  exit_filesystem_error("failed to create run directory", run_dir, e)
end

report_path = File.join(run_dir, "report.md")
commands_path = File.join(run_dir, "commands.log")

begin
  File.write(report_path, report)
rescue SystemCallError => e
  exit_filesystem_error("failed to write", report_path, e)
end

begin
  File.write(commands_path, "# Commands for #{scenario}\n")
rescue SystemCallError => e
  exit_filesystem_error("failed to write", commands_path, e)
end

puts run_dir
