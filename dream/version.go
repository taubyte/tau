package dream

import (
	"fmt"
	"runtime/debug"
)

// Version is what `dream --version` prints and GET /version returns.
type Version struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// LinkVersion and LinkCommit are set by -ldflags -X and win over build info.
var LinkVersion, LinkCommit string

// BuildVersion reads the version and commit Go embedded in the binary.
func BuildVersion() Version {
	v := versionFrom(debug.ReadBuildInfo())
	if LinkVersion != "" {
		v.Version = LinkVersion
	}
	if LinkCommit != "" {
		v.Commit = LinkCommit
	}
	return v
}

func versionFrom(info *debug.BuildInfo, ok bool) Version {
	v := Version{Version: "unknown", Commit: "unknown"}
	if !ok || info == nil {
		return v
	}
	if info.Main.Version != "" {
		v.Version = info.Main.Version
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			v.Commit = s.Value
		}
	}
	return v
}

func (v Version) String() string {
	commit := v.Commit
	if len(commit) > 7 {
		commit = commit[:7]
	}
	return fmt.Sprintf("dream %s (commit %s)", v.Version, commit)
}
