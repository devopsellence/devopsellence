package version

import (
	"fmt"
	"runtime"
	"strings"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("%s (%s, %s)", Version, Commit, Date)
}

func UserAgent() string {
	cliVersion := strings.TrimSpace(Version)
	if cliVersion == "" {
		cliVersion = "dev"
	}
	return fmt.Sprintf("devopsellence-cli/%s (%s; %s)", cliVersion, runtime.GOOS, runtime.GOARCH)
}
