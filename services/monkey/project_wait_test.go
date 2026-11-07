package monkey

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/services/monkey/jobs"
)

// A new repository's project shows up in this node's TNS a moment after
// Patrick saw it; the job waits for it instead of failing its first try.
func TestProjectLookupWaitsForANewRepository(t *testing.T) {
	calls := 0
	id, err := waitForProjectID(context.Background(), func() (string, error) {
		if calls++; calls < 3 {
			return "", errors.New("response from lookup repository `1` not an array or empty: `[]`")
		}
		return "project-1", nil
	})
	if err != nil || id != "project-1" || calls != 3 {
		t.Fatalf("got %q, %v after %d lookups", id, err, calls)
	}
}

// A repository no project claims still fails, after jobs.TnsRetryFor.
func TestProjectLookupGivesUp(t *testing.T) {
	defer func(d time.Duration) { jobs.TnsRetryFor = d }(jobs.TnsRetryFor)
	jobs.TnsRetryFor = 1500 * time.Millisecond
	start := time.Now()
	_, err := waitForProjectID(context.Background(), func() (string, error) { return "", errors.New("empty") })
	if err == nil || time.Since(start) > 4*time.Second {
		t.Fatalf("err %v after %s", err, time.Since(start))
	}
}

type lateProjects struct {
	auth.Projects
	calls int
}

func (l *lateProjects) Get(id string) *auth.Project {
	if l.calls++; l.calls < 3 {
		return nil
	}
	return &auth.Project{Id: id}
}

type lateAuth struct{ projects *lateProjects }

func (l lateAuth) Projects() auth.Projects { return l.projects }

// The project record can reach this node after the job does; the job waits
// for it instead of failing with "project not found".
func TestProjectRecordWaitsForANewProject(t *testing.T) {
	projects := &lateProjects{}
	p, err := waitForProject(context.Background(), lateAuth{projects}, "project-1")
	if err != nil || p == nil || p.Id != "project-1" || projects.calls != 3 {
		t.Fatalf("got %v, %v after %d reads", p, err, projects.calls)
	}
}
