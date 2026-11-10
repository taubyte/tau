package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/fxamacker/cbor/v2"
	commonIface "github.com/taubyte/tau/core/services/patrick"
	http "github.com/taubyte/tau/pkg/http"
)

// jobStore is a job store with List and Put, for code that indexes and scans.
type jobStore struct {
	*fakeJobs
	puts int
	mu   sync.Mutex
}

func (d *jobStore) Put(_ context.Context, key string, v []byte) error {
	d.mu.Lock()
	d.puts++
	d.mu.Unlock()
	d.fakeJobs.lock.Lock()
	defer d.fakeJobs.lock.Unlock()
	d.keys[key] = v
	return nil
}

func (d *jobStore) List(_ context.Context, prefix string) ([]string, error) {
	d.fakeJobs.lock.Lock()
	defer d.fakeJobs.lock.Unlock()
	var keys []string
	for k := range d.keys {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func finishedJob(t *testing.T, db *jobStore, id string, repo int, logs map[string]string) {
	t.Helper()
	job := &commonIface.Job{Id: id, Logs: logs}
	job.Meta.Repository.ID = repo
	data, err := cbor.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	db.keys["/archive/jobs/"+id] = data
}

func TestLogAccess(t *testing.T) {
	db := &jobStore{fakeJobs: &fakeJobs{keys: map[string][]byte{}}}
	finishedJob(t, db, "mine", 41, map[string]string{"mine": "bafy-mine"})
	finishedJob(t, db, "theirs", 42, map[string]string{"theirs": "bafy-theirs"})
	srv := &PatrickService{db: db}
	if err := srv.backfillLogOwners(context.Background()); err != nil {
		t.Fatal(err)
	}
	served := 0
	handler := srv.withLogAccess(func(http.Context) (interface{}, error) { served++; return "log", nil })
	owner := &fakeRepos{perms: map[string]map[string]bool{"41": {"push": true}, "42": {"pull": true}}}

	for _, c := range []struct {
		name, cid string
		want      error
	}{
		{"own log", "bafy-mine", nil},
		{"another project's log", "bafy-theirs", errNoJobAccess},
		{"a log no job claims", "bafy-nobody", errLogNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := served
			_, err := handler(accessCtx("log-token-"+c.name, owner, map[string]any{"cid": c.cid}))
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if (served > before) != (c.want == nil) {
				t.Fatalf("served=%v with %v", served > before, err)
			}
		})
	}
}

// A log stored before owners were recorded is found after the one-time
// backfill, and never by a scan on request.
func TestLogOwnerIsRecordedForOldLogs(t *testing.T) {
	db := &jobStore{fakeJobs: &fakeJobs{keys: map[string][]byte{}}}
	finishedJob(t, db, "old", 7, map[string]string{"old": "bafy-old", "fn": "bafy-old-fn"})
	srv := &PatrickService{db: db}

	if _, err := srv.logOwner(context.Background(), "bafy-old-fn"); err != errLogNotFound {
		t.Fatalf("an unrecorded log must not be looked up on request, got %v", err)
	}
	if err := srv.backfillLogOwners(context.Background()); err != nil {
		t.Fatal(err)
	}

	jid, err := srv.logOwner(context.Background(), "bafy-old-fn")
	if err != nil || jid != "old" {
		t.Fatalf("got %q, %v", jid, err)
	}
	if string(db.keys[logOwnerPrefix+"bafy-old"]) != "old" || string(db.keys[logOwnerPrefix+"bafy-old-fn"]) != "old" {
		t.Fatal("owners not recorded")
	}

	delete(db.keys, "/archive/jobs/old")
	if jid, err := srv.logOwner(context.Background(), "bafy-old"); err != nil || jid != "old" {
		t.Fatalf("recorded owner not used: %q, %v", jid, err)
	}
}

func TestRecordLogOwnerSkipsEmpty(t *testing.T) {
	db := &jobStore{fakeJobs: &fakeJobs{keys: map[string][]byte{}}}
	srv := &PatrickService{db: db}
	if err := srv.recordLogOwner(context.Background(), "j", map[string]string{"a": "bafy-a", "b": ""}); err != nil {
		t.Fatal(err)
	}
	if db.puts != 1 {
		t.Fatalf("%d puts", db.puts)
	}
}
