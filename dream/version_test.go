package dream

import (
	"runtime/debug"
	"testing"
)

func TestVersionFrom(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "a1ff405f0123456789"}}}
	if got := versionFrom(info, true).String(); got != "dream v1.2.3 (commit a1ff405)" {
		t.Fatal(got)
	}
	if got := versionFrom(nil, false).String(); got != "dream unknown (commit unknown)" {
		t.Fatal(got)
	}
}

func TestLinkVersionWins(t *testing.T) {
	LinkVersion, LinkCommit = "v9.9.9-fork.1", "abcdef1234"
	defer func() { LinkVersion, LinkCommit = "", "" }()
	if got := BuildVersion().String(); got != "dream v9.9.9-fork.1 (commit abcdef1)" {
		t.Fatal(got)
	}
}
