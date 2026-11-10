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
