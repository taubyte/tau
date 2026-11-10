package common

import (
	"slices"
	"testing"
)

// A push under a path must reach whoever cached a fetch of the path above it.
func TestChannelsForReachAncestors(t *testing.T) {
	push := ChannelsFor("resolve", "repo", "github", "1446329778")
	for _, ancestor := range [][]string{
		{"resolve", "repo", "github"},
		{"resolve", "repo"},
		{"resolve", "repo", "github", "1446329778"},
	} {
		if ch := GetChannelFor(ancestor...); !slices.Contains(push, ch) {
			t.Errorf("a fetch of %v listens on %q, which a push never reaches: %v", ancestor, ch, push)
		}
	}
	if len(ChannelsFor("only")) != 0 {
		t.Error("a one-segment key has no push channel")
	}
}
