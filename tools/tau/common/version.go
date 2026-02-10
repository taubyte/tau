package common

import (
	"fmt"
	"runtime/debug"
)

const defaultVersion = "0.1"

func init() {
	Version, Commit = versionFromBuildInfo()
}

// Version is the semantic version (from build info or default).
var Version string

// Commit is the VCS revision from runtime/debug (empty if not available).
var Commit string

func versionFromBuildInfo() (version, commit string) {
	version = defaultVersion
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version, ""
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			commit = s.Value
			if len(commit) > 7 {
				commit = commit[:7]
			}
			break
		}
	}
	return version, commit
}

// VersionLine returns the full line for "tau version".
func VersionLine() string {
	if Commit == "" {
		return fmt.Sprintf("Taubyte CLI version %s", Version)
	}
	return fmt.Sprintf("Taubyte CLI version %s (commit %s)", Version, Commit)
}
