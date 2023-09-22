package service

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/fxamacker/cbor/v2"
	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/patrick"
	dreamland "github.com/taubyte/tau/libdream"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	protocolsCommon "github.com/taubyte/tau/protocols/common"

	_ "github.com/taubyte/tau/protocols/auth"
)

func TestPatrick(t *testing.T) {
	t.Skip("Needs to be redone")
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"patrick": {},
			"auth":    {Others: map[string]int{"secure": 1}},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	_patrick := u.Patrick()

	// Make sure no jobs are stored already
	db := _patrick.KV()
	jobs, err := db.List(u.Context(), "/jobs/")
	if err != nil {
		t.Error("Failed calling list after error: ", err)
		return
	}

	if len(jobs) != 0 {
		t.Error("Should be an empty list of jobs got len of: ", len(jobs))
		return
	}

	mockAuthURL, err := u.GetURLHttps(u.Auth().Node())
	if err != nil {
		t.Error(err)
		return
	}

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	if err != nil {
		t.Error(err)
		return
	}

	err = commonTest.RegisterTestRepositories(u.Context(), mockAuthURL, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	protocolsCommon.FakeSecret = true
	err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	jobs, err = db.List(u.Context(), "/jobs/")
	if err != nil {
		t.Error("Failed calling list after error: ", err)
		return
	}

	if len(jobs) != 1 {
		t.Errorf("Should have one job stored in db got %d jobs", len(jobs))
		return
	}
	job_byte, err := db.Get(u.Context(), jobs[0])
	if err != nil {
		t.Errorf("Failed getting job error: %v", err)
		return
	}
	var job patrick.Job
	err = cbor.Unmarshal(job_byte, &job)
	if err != nil {
		t.Errorf("Failed unmarshal error: %v", err)
		return
	}

	err = compareJobToPayload(job.Meta, commonTest.ConfigPayload)
	if err != nil {
		t.Error(err)
		return
	}
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
