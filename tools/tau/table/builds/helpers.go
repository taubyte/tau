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

func row(authClient *authHttp.Client, job *patrick.Job, timeZone *time.Location, showCommit bool) (table.Row, error) {
	unixTime := time.Unix(job.Timestamp, 0).In(timeZone)
	date := unixTime.Format("01/02/06")
	time := unixTime.Format("3:04 PM")

	repo, err := authClient.GetRepositoryById(fmt.Sprintf("%d", job.Meta.Repository.ID))
	if err != nil {
		return nil, err
	}

	repoType := "Unknown"
	name := repo.Get().Name()
	nameSplit := strings.SplitN(name, "_", 3)
	if nameSplit[0] == "tb" {
		switch nameSplit[1] {
		case "library", "website", "code":
			repoType = cases.Title(language.English).String(nameSplit[1])
		default:
			repoType = "Config"
		}
	}

	var lastColumn interface{}
	if showCommit {
		lastColumn = job.Meta.HeadCommit.ID
	} else {
		lastColumn = job.Id
	}

	return table.Row{
		job.Status.Unicode(),
		date + "\n" + time,
		repoType,
		lastColumn,
	}, nil
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
