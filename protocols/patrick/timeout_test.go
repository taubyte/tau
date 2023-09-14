package service

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	dreamland "github.com/taubyte/tau/libdream"
	protocolCommon "github.com/taubyte/tau/protocols/common"
	"gotest.tools/v3/assert"
)

func TestTimeout(t *testing.T) {
	t.Skip("Using an old token/project")
	protocolCommon.TimeoutTest = true
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"patrick": {Others: map[string]int{"delay": 1}},
			"auth":    {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					Patrick: &commonIface.ClientConfig{},
					Monkey:  &commonIface.ClientConfig{},
					TNS:     &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	DefaultReAnnounceJobTime = 60 * time.Second
	DefaultReAnnounceFailedJobsTime = 60 * time.Second

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	// Push two jobs
	go func() {
		err := u.RunFixture("createProjectWithJobs")
		if err != nil {
			t.Error(err)
		}
	}()

	// Make sure both jobs are registered
	attemptsList := 0
	var jobs []string
	patrick, err := simple.Patrick()
	assert.NilError(t, err)

	for {
		if attemptsList == 10 {
			t.Error("Max attempts for list reached")
			return
		}
		jobs, _ = patrick.List()
		if len(jobs) == 2 {
			break
		}
		attemptsList++
		time.Sleep(3 * time.Second)

	}

	// Make sure both jobs are locked
	attemptsIsLocked := 0
	for {
		if attemptsIsLocked == 10 {
			t.Error("Max attempts isLocked reached")
			return
		}
		lockCounter := 0
		for _, id := range jobs {
			locked, _ := patrick.IsLocked(id)

			if locked == true {
				lockCounter++
			}
		}
		if lockCounter == 2 {
			break
		}
		attemptsIsLocked++
		time.Sleep(3 * time.Second)
	}

	// Time to timeout and circle 2 times
	time.Sleep(30 * time.Second)

	for _, id := range jobs {
		job, err := patrick.Get(id)
		if err != nil {
			t.Error(err)
			return
		}

		if job.Attempt != 2 {
			t.Errorf("Should have timedout and attempted 2 times got %d attemps", job.Attempt)
			return
		}
	}
}
