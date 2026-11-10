package buildsTable

import (
	"fmt"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	authHttp "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/core/services/patrick"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// repoNames looks a repository up once per listing: a project has a few
// repositories and many jobs, and each lookup is a call to GitHub that spends
// the token's hourly limit and took ~0.4s.
type repoNames map[string]string

func (names repoNames) get(id string, lookup func(id string) (string, error)) (string, error) {
	if name, ok := names[id]; ok {
		return name, nil
	}
	name, err := lookup(id)
	if err != nil {
		return "", err
	}
	names[id] = name
	return name, nil
}

func row(authClient *authHttp.Client, names repoNames, job *patrick.Job, timeZone *time.Location, showCommit bool) (table.Row, error) {
	unixTime := time.Unix(job.Timestamp, 0).In(timeZone)
	date := unixTime.Format("01/02/06")
	time := unixTime.Format("3:04 PM")

	name, err := names.get(fmt.Sprintf("%d", job.Meta.Repository.ID), func(id string) (string, error) {
		repo, err := authClient.GetRepositoryById(id)
		if err != nil {
			return "", err
		}
		return repo.Get().Name(), nil
	})
	if err != nil {
		return nil, err
	}

	repoType := "Unknown"
	nameSplit := strings.SplitN(name, "_", 3)
	if nameSplit[0] == "tb" {
		switch nameSplit[1] {
		case "library", "website", "code":
			repoType = cases.Title(language.English).String(nameSplit[1])
		default:
			repoType = "Config"
		}
	}

	if showCommit {
		return table.Row{
			statusMark(job),
			date + "\n" + time,
			repoType,
			job.Meta.HeadCommit.ID,
		}, nil
	}
	return table.Row{
		statusMark(job),
		date + "\n" + time,
		repoType,
		job.Id,
	}, nil
}

// statusMark is the job's status; an open job with an attempt used failed and
// is being run again.
func statusMark(job *patrick.Job) string {
	if job.Status == patrick.JobStatusOpen && job.Attempt > 0 {
		return "↻ retry"
	}
	return job.Status.Unicode()
}

type jobArray []*patrick.Job

func (a jobArray) Len() int           { return len(a) }
func (a jobArray) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a jobArray) Less(i, j int) bool { return a[i].Timestamp > a[j].Timestamp }
func (a jobArray) String() (s string) {
	sep := "" // for printing separating commas
	for _, el := range a {
		s += sep
		sep = ", "
		s += fmt.Sprintf("%d", el.Timestamp)
	}
	return
}
