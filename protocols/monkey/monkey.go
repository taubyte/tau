package monkey

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/services/patrick"
	hoarderClient "github.com/taubyte/tau/clients/p2p/hoarder"
	protocolCommon "github.com/taubyte/tau/protocols/common"
)

func (m *Monkey) Run() {
	m.Status = patrick.JobStatusLocked
	isLocked, err := m.Service.patrickClient.IsLocked(m.Id)
	if !isLocked {
		m.addDebugMsg(log.LevelError, "Locking job %s failed", m.Id)
	}
	if err != nil {
		m.addDebugMsg(log.LevelError, "Checking if locked job %s failed with: %s", m.Id, err.Error())
	}

	if err = m.RunJob(); err != nil {
		m.addDebugMsg(log.LevelError, "Running job `%s` failed with error: %s", m.Id, err.Error())
	} else {
		m.addDebugMsg(log.LevelInfo, "Running job `%s` was successful", m.Id)
	}

	m.logFile.Seek(0, io.SeekStart)
	cid, err0 := m.storeLogs(m.logFile)
	if err0 != nil {
		logger.Errorf("Writing cid of job `%s` failed: %s", m.Id, err0.Error())
	}

	m.Job.Logs[m.Job.Id] = cid
	if err != nil {
		if strings.Contains(err.Error(), protocolCommon.RetryErrorString) {
			delete(m.Service.monkeys, m.Job.Id)

			if err = m.Service.patrickClient.Unlock(m.Id); err != nil {
				logger.Errorf("Unlocking job failed `%s` failed with: %s", m.Id, err.Error())
			}
		} else {
			if err = m.Service.patrickClient.Failed(m.Id, m.Job.Logs, m.Job.AssetCid); err != nil {
				logger.Errorf("Marking job failed `%s` failed with: %s", m.Id, err.Error())
			}
			m.Status = patrick.JobStatusFailed
		}
	} else {
		if err = m.Service.patrickClient.Done(m.Id, m.Job.Logs, m.Job.AssetCid); err != nil {
			logger.Errorf("Marking job done `%s` failed: %s", m.Id, err.Error())
			m.Status = patrick.JobStatusFailed
		} else {
			m.Status = patrick.JobStatusSuccess
		}
	}

	hoarder, err := hoarderClient.New(m.Service.ctx, m.Service.node)
	if err != nil {
		logger.Error(err.Error())
	}

	// Stash the logs
	if _, err = hoarder.Stash(cid); err != nil {
		logger.Errorf("Hoarding cid `%s` of job `%s` failed: %s", cid, m.Id, err.Error())
	}

	m.LogCID = cid

	// Free the jobID from monkey
	if !protocolCommon.LocalPatrick {
		delete(m.Service.monkeys, m.Job.Id)
	}
}

func (s *Service) newMonkey(job *patrick.Job) (*Monkey, error) {
	jid := job.Id
	err := s.patrickClient.Lock(jid, uint32(protocolCommon.DefaultLockTime)) //5 minutes to complete a job
	if err != nil {
		return nil, err
	}

	randSleep()
	locked, err := s.patrickClient.IsLocked(jid)
	if err != nil {
		return nil, err
	}

	if locked {
		logFile, err := os.CreateTemp("/tmp", fmt.Sprintf("log-%s", jid))
		if err != nil {
			return nil, err
		}

		m := &Monkey{
			Id:      jid,
			Status:  patrick.JobStatusOpen,
			Service: s,
			Job:     job,
			logFile: logFile,
		}

		m.ctx, m.ctxC = context.WithCancel(s.ctx)
		s.monkeys[jid] = m
		return m, nil
	}

	return nil, fmt.Errorf("didn't actually lock")
}

// Random sleep so job is unlocked randomly.
func randSleep() error {
	// Using int 1<<53 as per math/rand documentation for 53 bit int.
	n, err := rand.Int(rand.Reader, big.NewInt(1<<53))
	if err != nil {
		return fmt.Errorf("generating random int failed with: %s", err)
	}

	// Convert to random value between 0 and 1 nanoseconds to value between 0 and 10 seconds
	time.Sleep(time.Duration(float64(n.Int64()) / float64(1<<53) * 10000000000))
	return nil
}
