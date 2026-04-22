package mock

import (
	"errors"
	"fmt"
	"testing"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	kvdbIface "github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
)

type Starfish struct {
	Jobs map[string]*patrick.Job
}

func (s *Starfish) Close() {
	s.Jobs = nil
}

// AddJob registers a job for tests using the Patrick mock. The job is stored with
// status JobStatusOpen so Dequeue can assign it to a monkey.
func (s *Starfish) AddJob(t *testing.T, _ peer.Node, job *patrick.Job) error {
	t.Helper()
	if job == nil {
		return errors.New("nil job")
	}
	if job.Id == "" {
		return errors.New("job id empty")
	}
	if job.Logs == nil {
		job.Logs = make(map[string]string)
	}
	if job.AssetCid == nil {
		job.AssetCid = make(map[string]string)
	}
	if job.Timestamp == 0 {
		job.Timestamp = time.Now().UnixNano()
	}
	s.Jobs[job.Id] = job
	return nil
}

func (s *Starfish) Peers(...peerCore.ID) patrick.Client {
	return s
}

func (s *Starfish) DatabaseStats() (kvdbIface.Stats, error) {
	return kvdb.NewStats(), nil
}

func (s *Starfish) Dequeue() (*patrick.Job, error) {
	for _, job := range s.Jobs {
		if job.Status == patrick.JobStatusOpen {
			job.Status = patrick.JobStatusLocked
			return job, nil
		}
	}
	return nil, nil
}

func (s *Starfish) IsAssigned(jid string) (bool, error) {
	job, ok := s.Jobs[jid]
	if !ok {
		return false, nil
	}
	return job.Status == patrick.JobStatusLocked, nil
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
	job, ok := s.Jobs[jid]
	if !ok {
		return fmt.Errorf("can't find job %s", jid)
	}
	job.Logs = cid_log
	job.Status = patrick.JobStatusFailed
	return nil
}

func (s *Starfish) Get(jid string) (*patrick.Job, error) {
	job, ok := s.Jobs[jid]
	if !ok {
		return nil, errors.New("job not found")
	}
	return job, nil
}

func (s *Starfish) List() (ret []string, err error) {
	for k := range s.Jobs {
		ret = append(ret, k)
	}
	return
}

func (s *Starfish) Timeout(jid string) error {
	return fmt.Errorf("not implemented")
}

func (s *Starfish) Cancel(jid string, cid_log map[string]string) (interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}
