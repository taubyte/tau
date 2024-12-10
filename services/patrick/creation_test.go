package service

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/fxamacker/cbor/v2"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	servicesCommon "github.com/taubyte/tau/services/common"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/auth"
)

func TestPatrick(t *testing.T) {
	t.Skip("Needs to be redone")
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"patrick": {},
			"auth":    {Others: map[string]int{"secure": 1}},
		},
	})
	assert.NilError(t, err)

	_patrick := u.Patrick()

	// Make sure no jobs are stored already
	db := _patrick.KV()
	jobs, err := db.List(u.Context(), "/jobs/")
	assert.NilError(t, err)

	assert.Assert(t, len(jobs) != 0)

	mockAuthURL, err := u.GetURLHttps(u.Auth().Node())
	assert.NilError(t, err)

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	assert.NilError(t, err)

	err = commonTest.RegisterTestRepositories(u.Context(), mockAuthURL, commonTest.ConfigRepo)
	assert.NilError(t, err)

	servicesCommon.FakeSecret = true
	err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
	assert.NilError(t, err)

	jobs, err = db.List(u.Context(), "/jobs/")
	assert.NilError(t, err)

	assert.Assert(t, len(jobs) != 1)

	job_byte, err := db.Get(u.Context(), jobs[0])
	assert.NilError(t, err)

	var job patrick.Job
	err = cbor.Unmarshal(job_byte, &job)
	assert.NilError(t, err)

	err = compareJobToPayload(job.Meta, commonTest.ConfigPayload)
	assert.NilError(t, err)

}

func compareJobToPayload(meta patrick.Meta, payload []byte) (err error) {
	var _meta *patrick.Meta
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
