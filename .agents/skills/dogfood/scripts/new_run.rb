#!/usr/bin/env ruby
# frozen_string_literal: true

require "fileutils"
require "open3"
require "optparse"
require "time"
require "tmpdir"

options = {
  root: Dir.pwd,
  out: File.join(Dir.tmpdir, "devopsellence-dogfood"),
  version: nil
}

parser = OptionParser.new do |opts|
  opts.banner = "Usage: new_run.rb SCENARIO [--version VERSION] [--root PATH] [--out PATH]"
  opts.on("--version VERSION", "devopsellence version to dogfood; omit for default stable") { |value| options[:version] = value }
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

scenario = ARGV.join(" ")
if scenario.empty?
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

version = options[:version]&.strip
if version&.empty?
  warn "VERSION must not be empty"
  warn parser
  exit 64
end
if version&.match?(/[[:cntrl:]]/)
  warn "VERSION must not contain control characters"
  warn parser
  exit 64
end
if version && !version.match?(/\A[A-Za-z0-9][A-Za-z0-9._-]*\z/)
  warn "VERSION may contain only letters, digits, dots, underscores, and dashes"
  warn parser
  exit 64
end

target_version = version || "default stable"
install_command = if version
  "curl -fsSL https://www.devopsellence.com/lfg.sh?version=#{version} | bash"
else
  "curl -fsSL https://www.devopsellence.com/lfg.sh | bash"
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
  .sub("Target version:", "Target version: #{target_version}")
  .sub("Install command:", "Install command: #{install_command}")
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

commands_template = <<~LOG
  # Commands for #{scenario}
  # Target version: #{target_version}
  # Install command:
  #{install_command}

  # Record each meaningful step in this format:
  #
  # ## <ISO-8601 timestamp> <short step name>
  # cwd: <working directory>
  # agent intent: <why the agent is doing this>
  # user approval: <not needed | requested | granted | denied>
  # command: <command with secrets redacted>
  # exit: <exit code>
  # output excerpt:
  # <minimal stdout/stderr or JSON proving the result>
  # agent interpretation:
  # <what the agent concluded and next action>
  #
  # Use placeholders such as $TOKEN, <redacted>, or <private-host> instead of secret values or private identifiers.
LOG

begin
  File.write(commands_path, commands_template)
rescue SystemCallError => e
  exit_filesystem_error("failed to write", commands_path, e)
end

puts run_dir
