#!/usr/bin/env ruby
# frozen_string_literal: true

require "fileutils"
require "open3"
require "optparse"
require "time"
require "tmpdir"

options = {
  root: Dir.pwd,
  out: File.join(Dir.tmpdir, "devopsellence-dogfood-solo"),
  version: nil,
  mode: nil
}

parser = OptionParser.new do |opts|
  opts.banner = "Usage: new_run.rb SCENARIO [--version VERSION] [--mode MODE] [--root PATH] [--out PATH]"
  opts.on("--version VERSION", "devopsellence version/commit/PR to dogfood; omit when unknown") { |value| options[:version] = value }
  opts.on("--mode MODE", "validation mode: local-build, official-artifact, installed-stable, or unknown") { |value| options[:mode] = value }
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
options[:root] = File.expand_path(options[:root])

scenario = ARGV.join(" ").strip
if scenario.empty?
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

def clean_option!(name, value, pattern)
  return nil if value.nil?

  value = value.strip
  if value.empty?
    warn "#{name} must not be empty"
    exit 64
  end
  if value.match?(/[[:cntrl:]]/) || !value.match?(pattern)
    warn "#{name} contains unsupported characters"
    exit 64
  end
  value
end

version = clean_option!("VERSION", options[:version], /\A[A-Za-z0-9][A-Za-z0-9._\/-]*\z/)
mode = clean_option!("MODE", options[:mode], /\A[A-Za-z0-9][A-Za-z0-9._-]*\z/) || "unknown"
target_version = version || "unspecified"

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

skill_root = File.expand_path("..", __dir__)
report_template_path = File.join(skill_root, "references", "report-template.md")
commands_template_path = File.join(skill_root, "references", "commands-log-template.md")

begin
  report = File.read(report_template_path)
rescue SystemCallError => e
  exit_filesystem_error("failed to read", report_template_path, e)
end

report = report
  .sub("Scenario:", "Scenario: #{scenario}")
  .sub("Target version/commit:", "Target version/commit: #{target_version}")
  .sub("Validation mode:", "Validation mode: #{mode}")
  .sub("Date:", "Date: #{now.iso8601}")
  .sub("Commit:", "Commit: #{commit} (#{branch})")
  .sub("Run path:", "Run path: #{run_dir}")

begin
  commands_template = File.read(commands_template_path)
rescue SystemCallError => e
  exit_filesystem_error("failed to read", commands_template_path, e)
end

commands_log = commands_template
  .gsub("{{scenario}}", scenario)
  .gsub("{{target_version}}", target_version)
  .sub("Validation mode: <local-build | official-artifact | installed-stable | unknown>", "Validation mode: #{mode}")
  .sub("Run started: <UTC timestamp>", "Run started: #{now.iso8601}")
  .sub("Run directory: <path>", "Run directory: #{run_dir}")

begin
  FileUtils.mkdir_p(options[:out])
  Dir.mkdir(run_dir)
  File.write(File.join(run_dir, "report.md"), report)
  File.write(File.join(run_dir, "commands.log"), commands_log)
  FileUtils.mkdir_p(File.join(run_dir, "evidence"))
rescue Errno::EEXIST
  warn "run directory already exists: #{run_dir}"
  exit 73
rescue SystemCallError => e
  exit_filesystem_error("failed to create dogfood run", run_dir, e)
end

puts run_dir
