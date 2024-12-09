package mock

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/fxamacker/cbor/v2"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	kvdbIface "github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"
)

type Starfish struct {
	Jobs map[string]*patrick.Job
}

func (s *Starfish) Close() {
	s.Jobs = nil
}

func (s *Starfish) Peers(...peerCore.ID) patrick.Client {
	return s
}

func (s *Starfish) DatabaseStats() (kvdbIface.Stats, error) {
	return kvdb.NewStats(), nil
}

func (s *Starfish) AddJob(t *testing.T, peerC peer.Node, job *patrick.Job) error {
	job_bytes, err := cbor.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job to add failed: %w", err)
	}

	s.Jobs[job.Id] = job

	err = peerC.PubSubPublish(context.TODO(), patrickSpecs.PubSubIdent, job_bytes)
	if err != nil {
		return fmt.Errorf("publish job failed: %w", err)
	}

	return nil
}

func (s *Starfish) Lock(jid string, eta uint32) error {
	job, ok := s.Jobs[jid]
	if !ok {
		return fmt.Errorf("can't find job %s", jid)
	}

	if job.Status != 0 {
		return fmt.Errorf("job `%s` already locked", jid)
	}
	job.Status = patrick.JobStatusLocked
	return nil
}

func (s *Starfish) IsLocked(jid string) (bool, error) {
	return (s.Jobs[jid].Status != 0), nil
}

func (s *Starfish) Done(jid string, cid_log map[string]string, assetCid map[string]string) error {
	job := s.Jobs[jid]
	if job != nil {
		job.Logs = cid_log
		job.Status = patrick.JobStatusSuccess
	}
	return nil
}

func (s *Starfish) Failed(jid string, cid_log map[string]string, assetCid map[string]string) error {
	job := s.Jobs[jid]
	job.Logs = cid_log
	job.Status = patrick.JobStatusFailed
	return nil
}

// added to satisfy the patrick interface
func (s *Starfish) Get(jid string) (*patrick.Job, error) {
	job, ok := s.Jobs[jid]
	if !ok {
		return nil, errors.New("job not found")
	}
	return job, nil
}

// added to satisfy the patrick interface
func (s *Starfish) List() (ret []string, err error) {
	for k := range s.Jobs {
		ret = append(ret, k)
	}
	return
}

// added to satisfy the patrick interface
func (s *Starfish) Unlock(jid string) error {
	return fmt.Errorf("not implemented")
}

// added to satisfy the patrick interface
func (s *Starfish) Timeout(jid string) error {
	return fmt.Errorf("not implemented")
}

// added to satisfy the patrick interface
func (s *Starfish) Cancel(jid string, cid_log map[string]string) (interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}
