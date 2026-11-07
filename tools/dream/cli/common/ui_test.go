package common

import (
	"errors"
	"strings"
	"testing"
)

func TestFriendly(t *testing.T) {
	list := func(names ...string) func() ([]string, error) {
		return func() ([]string, error) { return names, nil }
	}
	for _, tc := range []struct {
		raw       string
		universes func() ([]string, error)
		want      string
	}{
		{"GET -- `/les/miserables/default` do failed with: Get \"http://127.0.0.1:1421/x\": dial tcp 127.0.0.1:1421: connect: connection refused", nil,
			"Dream is not running at http://127.0.0.1:1421. Start it with `dream start`"},
		{"DELETE -- `/universe/b` do failed with: dial tcp 127.0.0.1:1421: connect: network is unreachable", nil,
			"Dream is not running"},
		{"GET -- `/les/miserables/u` failed with: universe `u` does not exit", list("test", "blackhole"),
			"universe `u` not found. Running universes: blackhole, test. Pick one, or create it with `dream new universe u`"},
		{"universe `u` not found", list(),
			"universe `u` not found. No universe is running. Create it with `dream new universe u`"},
		{"universe `u` does not exist", func() ([]string, error) { return nil, errors.New("down") },
			"universe `u` not found. Create it with `dream new universe u`"},
	} {
		got := Friendly(errors.New(tc.raw), tc.universes).Error()
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("%q\n got  %q\n want %q", tc.raw, got, tc.want)
		}
	}
	if Friendly(nil, nil) != nil {
		t.Error("nil error must stay nil")
	}
	if other := errors.New("something else"); Friendly(other, nil) != other {
		t.Error("other errors must pass through unchanged")
	}
}

func TestSuggest(t *testing.T) {
	names := []string{"new", "start", "inject", "kill", "status", "reset", "version"}
	for in, want := range map[string]string{"sttaus": "status", "strat": "start", "kil": "kill", "xyzzyqq": ""} {
		if got := Suggest(in, names); got != want {
			t.Errorf("Suggest(%q) = %q, want %q", in, got, want)
		}
	}
}
