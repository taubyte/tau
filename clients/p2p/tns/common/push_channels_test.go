package common

import (
	"slices"
	"testing"
)

// The config compiler pushes the routing index at the root path. A gateway
// that cached a lookup inside it must hear of the push, or a new website is
// served the old routes for up to two minutes.
func TestPushAtTheRootReachesKeysInside(t *testing.T) {
	index := map[string]interface{}{
		"domains": map[string]interface{}{
			"abc.blackhole.localtau": map[string]interface{}{
				"links": map[interface{}]interface{}{"website": map[string]interface{}{"site5": "x"}},
			},
		},
	}
	push := ChannelsForPush([]string{}, index)
	for _, fetched := range [][]string{
		{"domains"},
		{"domains", "abc.blackhole.localtau"},
		{"domains", "abc.blackhole.localtau", "links"},
		{"domains", "abc.blackhole.localtau", "links", "website", "site5"},
	} {
		if ch := GetChannelFor(fetched...); !slices.Contains(push, ch) {
			t.Errorf("a fetch of %v listens on %q, which this push never reaches: %v", fetched, ch, push)
		}
	}
}

func TestPushOfAValueKeepsItsChannels(t *testing.T) {
	path := []string{"resolve", "repo", "github", "1"}
	if got, want := ChannelsForPush(path, "cid"), ChannelsFor(path...); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
