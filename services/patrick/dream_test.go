//go:build dreaming

package service_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	commonIface "github.com/taubyte/tau/core/common"
	patrickCore "github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	spec "github.com/taubyte/tau/pkg/specs/common"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"
	servicesCommon "github.com/taubyte/tau/services/common"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/tns/dream"

	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
)

func TestDream_Dreaming(t *testing.T) {
	http.DefaultClient = commonTest.CreateHttpClient()

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"patrick": {},
			"auth":    {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	auth, err := simple.Auth()
	assert.NilError(t, err)

	_patrick := u.Patrick()

	db := _patrick.KV()
	jobs, err := db.List(u.Context(), "/jobs/")
	assert.NilError(t, err)
	assert.Equal(t, len(jobs), 0)

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	assert.NilError(t, err)

	err = commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.ConfigRepo)
	assert.NilError(t, err)

	servicesCommon.FakeSecret = true

	t.Run("Creation", func(t *testing.T) {
		err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
		assert.NilError(t, err)

		time.Sleep(1 * time.Second)

		jobs, err = db.List(u.Context(), "/jobs/")
		assert.NilError(t, err)
		assert.Equal(t, len(jobs), 1)

		job_byte, err := db.Get(u.Context(), jobs[0])
		assert.NilError(t, err)

		var job patrickCore.Job
		err = cbor.Unmarshal(job_byte, &job)
		assert.NilError(t, err)

		err = compareJobToPayload(job.Meta, commonTest.ConfigPayload)
		assert.NilError(t, err)
	})

	t.Run("ReportSsh", func(t *testing.T) {
		err = u.RunFixture("createProjectWithJobs")
		assert.NilError(t, err)

		attempts := 0
		var job *patrickCore.Job
		patrick, err := simple.Patrick()
		assert.NilError(t, err)

		for {
			attempts++
			assert.Assert(t, attempts < 20)

			jobs, err := patrick.List()
			assert.NilError(t, err)
			if len(jobs) < 2 {
				continue
			}

			job, err = patrick.Get(jobs[0])
			assert.NilError(t, err)
			if job != nil {
				break
			}

			time.Sleep(1 * time.Second)
		}

		tns, err := simple.TNS()
		assert.NilError(t, err)

		resp, err := tns.Fetch(spec.NewTnsPath([]string{"resolve", "repo", "github", fmt.Sprintf("%d", job.Meta.Repository.ID), "ssh"}))
		if err != nil {
			t.Error(err)
			return
		}

		if resp.Interface() != job.Meta.Repository.SSHURL {
			t.Errorf("Response from tns does not match data from patrick, got `%v` != `%s`", resp, job.Meta.Repository.SSHURL)
			return
		}
	})

	t.Run("ReAnnounce", func(t *testing.T) {
		// Create new jobs for this test
		err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
		assert.NilError(t, err)

		time.Sleep(1 * time.Second)

		patrick, err := simple.Patrick()
		assert.NilError(t, err)

		jobs, err := patrick.List()
		assert.NilError(t, err)
		assert.Assert(t, len(jobs) > 0, "No jobs available after creating them")

		job_byte, err := patrick.Get(jobs[0])
		assert.NilError(t, err)

		patrickClient, err := simple.Patrick()
		assert.NilError(t, err)

		// Test reannouncement by subscribing to patrick pubsub
		reannounceCount := 0

		// Subscribe to patrick pubsub to listen for reannouncements
		reannounceChan := make(chan []byte, 10)
		err = u.Patrick().Node().PubSubSubscribe(patrickSpecs.PubSubIdent, func(msg *pubsub.Message) {
			reannounceChan <- msg.Data
		}, func(err error) {
			// Ignore errors
		})
		assert.NilError(t, err)

		// Lock job
		err = patrickClient.Lock(job_byte.Id, 10)
		assert.NilError(t, err)

		// Wait a second
		time.Sleep(1 * time.Second)

		// Fail job
		err = patrickClient.Failed(job_byte.Id, job_byte.Logs, nil)
		assert.NilError(t, err)

		// Wait for reannouncement on pubsub
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		select {
		case <-ctx.Done():
			t.Fatal("Timeout waiting for reannouncement")
		case data := <-reannounceChan:
			// Check if this is a reannouncement of our job
			var job patrickCore.Job
			err = cbor.Unmarshal(data, &job)
			assert.NilError(t, err)

			if job.Id == job_byte.Id {
				reannounceCount++
			}
		}

		// We should have received a reannouncement
		assert.Assert(t, reannounceCount > 0, "Should have received reannouncement on pubsub")
	})

}

func compareJobToPayload(meta patrickCore.Meta, payload []byte) (err error) {
	var _meta *patrickCore.Meta
	err = json.Unmarshal(payload, &_meta)
	if err != nil {
		return
	}

	type comparison struct {
		Before interface{}
		After  interface{}
		msg    string
	}

	compare := func(c *comparison) error {
		if !reflect.DeepEqual(c.Before, c.After) {
			return fmt.Errorf("%s doesn't match got %v expected %v", c.msg, c.Before, c.After)
		}

		return nil
	}

	comparisons := []*comparison{
		{Before: meta.Ref, After: _meta.Ref, msg: "Ref"},
		{Before: meta.Before, After: _meta.Before, msg: "Before"},
		{Before: meta.After, After: _meta.After, msg: "After"},
		{Before: meta.HeadCommit.ID, After: _meta.HeadCommit.ID, msg: "HeadCommit.ID"},
		{Before: meta.Repository.ID, After: _meta.Repository.ID, msg: "Repository.ID"},
		{Before: meta.Repository.SSHURL, After: _meta.Repository.SSHURL, msg: "Repository.SSHURL"},
	}

	for _, c := range comparisons {
		if err = compare(c); err != nil {
			return
		}
	}

	return
}
