package service_test

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	protocolCommon "github.com/taubyte/tau/services/common"
	patrick "github.com/taubyte/tau/services/patrick"
	"gotest.tools/v3/assert"
)

func TestTimeout(t *testing.T) {
	t.Skip("Using an old token/project")
	protocolCommon.TimeoutTest = true
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"patrick": {Others: map[string]int{"delay": 1}},
			"auth":    {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Patrick: &commonIface.ClientConfig{},
					Monkey:  &commonIface.ClientConfig{},
					TNS:     &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	patrick.DefaultReAnnounceJobTime = 60 * time.Second
	patrick.DefaultReAnnounceFailedJobsTime = 60 * time.Second

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
	patrick, err := simple.Patrick()
	assert.NilError(t, err)

	jobs := make([]string, 0)

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

			if locked {
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
