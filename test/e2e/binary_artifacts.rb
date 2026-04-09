# frozen_string_literal: true

module E2EBinaryArtifacts
  TARGETS = [
    [ "linux", "amd64" ],
    [ "linux", "arm64" ],
    [ "darwin", "amd64" ],
    [ "darwin", "arm64" ]
  ].freeze

  def prepare_binary_artifacts!
    prepare_binary_artifact!(
      root: @cli_root,
      version: @release_version,
      module_path: "github.com/devopsellence/cli/internal/version",
      build_time_field: "Date"
    )
    prepare_binary_artifact!(
      root: @agent_root,
      version: @release_version,
      module_path: "github.com/devopsellence/devopsellence/agent/internal/version",
      build_time_field: "BuildTime"
    )
  end

  def prepare_binary_artifact!(root:, version:, module_path:, build_time_field:)
    root = Pathname(root)
    dist_dir = root.join("dist", version)
    FileUtils.rm_rf(dist_dir)
    FileUtils.mkdir_p(dist_dir)

    commit = ENV.fetch("GIT_COMMIT", "").to_s.strip
    commit = capture!("git", "rev-parse", "--short=12", "HEAD", chdir: root.to_s).strip if commit.empty?

    build_time = ENV.fetch("BUILD_TIME", "").to_s.strip
    build_time = Time.now.utc.iso8601 if build_time.empty?

    ldflags = [
      "-s",
      "-w",
      "-X", "#{module_path}.Version=#{version}",
      "-X", "#{module_path}.Commit=#{commit}",
      "-X", "#{module_path}.#{build_time_field}=#{build_time}"
    ]

    checksums = []
    TARGETS.each do |goos, goarch|
      artifact = "#{goos}-#{goarch}"
      output = dist_dir.join(artifact)

      run!(
        go_binary, "build",
        "-trimpath",
        "-ldflags", ldflags.join(" "),
        "-o", output.to_s,
        "./cmd/devopsellence",
        chdir: root.to_s,
        timeout: 1200,
        env: {
          "CGO_ENABLED" => "0",
          "GOOS" => goos,
          "GOARCH" => goarch,
          "GOCACHE" => root.join(".gocache").to_s
        }
      )

      checksums << "#{Digest::SHA256.file(output).hexdigest}  #{artifact}"
    end

    dist_dir.join("SHA256SUMS").write(checksums.join("\n") + "\n")
  end
end
