package tests

import (
	"fmt"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/p2p/peer"
	kvdbIface "github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/services/patrick"

	"github.com/taubyte/tau/pkg/kvdb"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"
)

type starfish struct {
	Jobs map[string]*patrick.Job
}

func (s *starfish) Close() {
	s.Jobs = nil
}

func (s *starfish) DatabaseStats() (kvdbIface.Stats, error) {
	return kvdb.NewStats(), nil
}

func (s *starfish) AddJob(t *testing.T, peerC peer.Node, job *patrick.Job) error {
	job_bytes, err := cbor.Marshal(job)
	if err != nil {
		return fmt.Errorf("Marshal job to add failed: %w", err)
	}

	s.Jobs[job.Id] = job
	err = peerC.Messaging().Publish(patrickSpecs.PubSubIdent, job_bytes)
	if err != nil {
		return fmt.Errorf("Publish job failed: %w", err)
	}
	return nil
}

func (s *starfish) Lock(jid string, eta uint32) error {
	job, ok := s.Jobs[jid]
	if !ok {
		return fmt.Errorf("Can't find job %s", jid)
	}

	if job.Status != 0 {
		return fmt.Errorf("Job `%s` already locked", jid)
	}
	job.Status = patrick.JobStatusLocked
	return nil
}

func (s *starfish) IsLocked(jid string) (bool, error) {
	return (s.Jobs[jid].Status != 0), nil
}

func (s *starfish) Done(jid string, cid_log map[string]string, assetCid map[string]string) error {
	job := s.Jobs[jid]
	job.Logs = cid_log
	job.Status = patrick.JobStatusSuccess
	return nil
}

func (s *starfish) Failed(jid string, cid_log map[string]string, assetCid map[string]string) error {
	job := s.Jobs[jid]
	job.Logs = cid_log
	job.Status = patrick.JobStatusFailed
	return nil
}

// added to satisfy the patrick interface
func (s *starfish) Get(jid string) (*patrick.Job, error) {
	return nil, fmt.Errorf("Get Not Implemented")
}

// added to satisfy the patrick interface
func (s *starfish) List() ([]string, error) {
	return nil, fmt.Errorf("List Not Implemented")
}

// added to satisfy the patrick interface
func (s *starfish) Timeout(jid string) error {
	return fmt.Errorf("Not implemented")
}

// added to satisfy the patrick interface
func (s *starfish) Unlock(jid string) error {
	return fmt.Errorf("Not implemented")
}

// added to satisfy the patrick interface
func (s *starfish) Cancel(jid string, cid_log map[string]string) (interface{}, error) {
	return nil, fmt.Errorf("Not implemented")
}
