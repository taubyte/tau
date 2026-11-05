package service

import (
	"context"
	"slices"
	"testing"

	"github.com/fxamacker/cbor/v2"
	commonIface "github.com/taubyte/tau/core/services/patrick"
)

func TestNewestJobs(t *testing.T) {
	db := &fakeJobs{keys: map[string][]byte{}}
	for id, ts := range map[string]int64{"old": 1, "mid": 5, "new": 9, "running": 7} {
		loc := "/archive/jobs/"
		if id == "running" {
			loc = "/jobs/"
		}
		data, err := cbor.Marshal(&commonIface.Job{Id: id, Timestamp: ts})
		if err != nil {
			t.Fatal(err)
		}
		db.keys[loc+id] = data
	}
	srv := &PatrickService{db: db}

	got := srv.newestJobs(context.Background(), []string{"old", "mid", "new", "running", "gone"}, 3)
	if want := []string{"new", "running", "mid"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	all := srv.newestJobs(context.Background(), []string{"old", "gone", "new"}, 10)
	if want := []string{"new", "old", "gone"}; !slices.Equal(all, want) {
		t.Fatalf("unreadable job not last: %v", all)
	}
}
